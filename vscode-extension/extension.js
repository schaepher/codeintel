// codeintel VS Code 扩展（#233 最小可用，纯 JS 免编译）。
// 后端复用 codeintel CLI（--json 契约，docs/json-contract.md）——
// 扩展只做「输入 → execFile → 解析渲染」，不重复实现任何查询逻辑。
'use strict';

const vscode = require('vscode');
const { execFile } = require('child_process');
const path = require('path');

/** @type {vscode.OutputChannel} */
let channel;

function activate(context) {
  channel = vscode.window.createOutputChannel('codeintel');
  context.subscriptions.push(
    vscode.commands.registerCommand('codeintel.querySymbol', () => querySymbol()),
    vscode.commands.registerCommand('codeintel.impact', () => queryImpact()),
    vscode.commands.registerCommand('codeintel.updateIndex', () => updateIndex())
  );
  bindAutoUpdate(context); // #234：保存 .go 后自动更新索引
}

// shouldAutoUpdateFileName #234：保存事件是否触发索引更新（.go 文件 +
// 配置开关，默认开）。
function shouldAutoUpdateFileName(fileName, enabled) {
  return enabled !== false && String(fileName || '').endsWith('.go');
}

// bindAutoUpdate 保存 .go 文件后防抖 2s 自动增量更新（索引自动更新闭环）。
function bindAutoUpdate(context) {
  if (!vscode.workspace || !vscode.workspace.onDidSaveTextDocument) return;
  let timer = null;
  context.subscriptions.push(vscode.workspace.onDidSaveTextDocument(function (doc) {
    if (!shouldAutoUpdateFileName(doc && doc.fileName, vscode.workspace.getConfiguration('codeintel').get('autoUpdate', true))) return;
    if (timer) clearTimeout(timer);
    timer = setTimeout(function () { updateIndex(); }, 2000);
  }));
}

function deactivate() {}

// ---- 配置 ----

function cfg() {
  const c = vscode.workspace.getConfiguration('codeintel');
  let repo = c.get('repoPath', '');
  if (!repo && vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders.length) {
    repo = vscode.workspace.workspaceFolders[0].uri.fsPath;
  }
  return { bin: c.get('binaryPath', 'codeintel'), repo: repo || '.' };
}

// ---- CLI 调用 ----

// runCli 执行 codeintel 命令；json=true 时解析 --json 输出，失败返回
// 错误信息文本。
function runCli(args, json) {
  const c = cfg();
  return new Promise(function (resolve) {
    execFile(c.bin, args, { maxBuffer: 10 * 1024 * 1024 }, function (err, stdout, stderr) {
      if (err) {
        const msg = (stderr || err.message || '').trim();
        if (/ENOENT|not found/i.test(String(err.message))) {
          resolve({ error: '找不到 codeintel 可执行文件（配置 codeintel.binaryPath；安装见仓库 README）' });
          return;
        }
        resolve({ error: msg || String(err.message) });
        return;
      }
      if (json) {
        try {
          resolve({ data: JSON.parse(stdout) });
        } catch (e) {
          resolve({ error: 'CLI 输出非 JSON：' + stdout.slice(0, 500) });
        }
        return;
      }
      resolve({ text: stdout });
    });
  });
}

// ---- 命令实现 ----

async function querySymbol() {
  const sym = await vscode.window.showInputBox({
    prompt: '符号名或 canonical ID',
    placeHolder: '如 (Manager).Run 或 symbol:go:...:main'
  });
  if (!sym) return;
  const c = cfg();
  let r = await runCli(['query', 'symbol', sym, '--repo', c.repo, '--json'], true);
  // #236：多匹配（重名符号）→ QuickPick 候选 → 用 canonical ID 重查
  if (r.error) {
    const cands = parseCandidates(r.error);
    if (cands.length > 0) {
      const pick = await vscode.window.showQuickPick(cands.map(function (id) {
        return { label: shortID(id), description: id };
      }), { placeHolder: '多个匹配，选择符号（' + cands.length + ' 个）' });
      if (!pick) return;
      r = await runCli(['query', 'symbol', pick.description, '--repo', c.repo, '--json'], true);
    }
  }
  channel.clear();
  channel.appendLine('codeintel query symbol ' + sym + ' (repo: ' + c.repo + ')');
  channel.appendLine('='.repeat(50));
  if (r.error) {
    channel.appendLine('✗ ' + r.error);
    channel.show();
    return;
  }
  channel.appendLine(renderSymbol(r.data));
  channel.show();
  // #236：QuickPick 导航（符号定义 + 调用者/被调用者）→ 选中跳转
  const d = r.data;
  const items = [
    { label: '$(file-code) ' + d.name, description: '跳转定义', jump: function () { return openAt(d.file, d.line); } }
  ];
  (d.callers || []).forEach(function (f) {
    items.push({ label: '$(arrow-up) ' + shortID(f.id), description: '调用者', jump: function () { return resolveAndJump(f.id); } });
  });
  (d.callees || []).forEach(function (f) {
    items.push({ label: '$(arrow-down) ' + shortID(f.id), description: '被调用者', jump: function () { return resolveAndJump(f.id); } });
  });
  const pick = await vscode.window.showQuickPick(items, { placeHolder: '选择跳转目标' });
  if (pick && pick.jump) await pick.jump();
}

async function queryImpact() {
  const sym = await vscode.window.showInputBox({
    prompt: '改动前影响预判——符号名或 canonical ID',
    placeHolder: '如 main'
  });
  if (!sym) return;
  const c = cfg();
  const r = await runCli(['query', 'impact', sym, '--repo', c.repo, '--json'], true);
  channel.clear();
  channel.appendLine('codeintel query impact ' + sym + ' (repo: ' + c.repo + ')');
  channel.appendLine('='.repeat(50));
  if (r.error) {
    channel.appendLine('✗ ' + r.error);
    channel.show();
    return;
  }
  const d = r.data;
  channel.appendLine('目标: ' + d.target);
  const nodes = d.nodes || [];
  channel.appendLine('影响节点（' + nodes.length + ' 个）:');
  nodes.forEach(function (n) {
    channel.appendLine('  [' + n.kind + '] ' + n.name + (n.file ? '  ' + n.file + (n.line ? ':' + n.line : '') : ''));
  });
  channel.show();
  // #236：影响节点 QuickPick 跳转（impact 输出自带 file/line，直接跳）
  const items = nodes.filter(function (n) { return n.file; }).map(function (n) {
    return {
      label: '[' + n.kind + '] ' + n.name,
      description: n.file + (n.line ? ':' + n.line : ''),
      jump: function () { return openAt(n.file, n.line); }
    };
  });
  if (!items.length) return;
  const pick = await vscode.window.showQuickPick(items, { placeHolder: '选择影响节点跳转（' + items.length + ' 个）' });
  if (pick && pick.jump) await pick.jump();
}

// ---- #236 跳到定义 ----

// openAt 打开文件并定位行（绝对路径或相对仓库根）。
async function openAt(file, line) {
  if (!file) return;
  const c = cfg();
  const abs = path.isAbsolute(file) ? file : path.join(c.repo, file);
  const doc = await vscode.workspace.openTextDocument(abs);
  const editor = await vscode.window.showTextDocument(doc);
  if (line) {
    const r = new vscode.Range(line - 1, 0, line - 1, 0);
    editor.revealRange(r);
    editor.selection = new vscode.Selection(r.start, r.start);
  }
}

// resolveAndJump 调用者/被调用者跳转：fact 只有 canonical ID（无
// file/line）→ 按需二次解析 symbol 拿位置再跳（点哪个查哪个）。
async function resolveAndJump(id) {
  const c = cfg();
  const r = await runCli(['query', 'symbol', id, '--repo', c.repo, '--json'], true);
  if (r.error || !r.data) {
    vscode.window.showWarningMessage('codeintel: 无法解析 ' + shortID(id) + '（' + (r.error || '无结果') + '）');
    return;
  }
  await openAt(r.data.file, r.data.line);
}

// parseCandidates 从 CLI 多匹配错误文本提取候选 canonical ID 列表。
function parseCandidates(errText) {
  const out = [];
  const re = /(symbol:go:[^\s，,；;]+)/g;
  let m;
  while ((m = re.exec(String(errText || ''))) !== null) {
    if (out.indexOf(m[1]) < 0) out.push(m[1]);
  }
  return out;
}

async function updateIndex() {
  const c = cfg();
  channel.clear();
  channel.appendLine('codeintel update --repo ' + c.repo);
  channel.appendLine('='.repeat(50));
  const r = await runCli(['update', '--repo', c.repo], false);
  if (r.error) {
    channel.appendLine('✗ ' + r.error);
  } else {
    channel.appendLine(r.text);
  }
  channel.show();
}

// ---- 渲染 ----

function renderSymbol(d) {
  if (!d || !d.name) return '（无结果）';
  const lines = [];
  lines.push('符号: ' + d.name + ' [' + d.kind + ']');
  if (d.id) lines.push('ID:   ' + d.id);
  if (d.file) lines.push('位置: ' + d.file + (d.line ? ':' + d.line : ''));
  if (d.signature) lines.push('签名: ' + d.signature);
  if (d.doc) lines.push('文档: ' + d.doc);
  if (d.callers && d.callers.length) {
    lines.push('');
    lines.push('调用者（' + d.callers.length + '）:');
    d.callers.forEach(function (f) {
      lines.push('  ' + shortID(f.id) + '  (conf=' + f.confidence.toFixed(2) + ')');
    });
  }
  if (d.callees && d.callees.length) {
    lines.push('');
    lines.push('被调用者（' + d.callees.length + '）:');
    d.callees.forEach(function (f) {
      lines.push('  ' + shortID(f.id) + '  (conf=' + f.confidence.toFixed(2) + ')');
    });
  }
  return lines.join('\n');
}

// shortID 压缩 canonical ID 便于阅读（symbol:go:<pkg>:<name> → <name>）。
function shortID(id) {
  const i = String(id).lastIndexOf(':');
  return i >= 0 ? String(id).slice(i + 1) : String(id);
}

module.exports = { activate, deactivate, renderSymbol, shortID, shouldAutoUpdateFileName, parseCandidates, openAt };
