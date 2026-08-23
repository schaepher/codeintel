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
  const r = await runCli(['query', 'symbol', sym, '--repo', c.repo, '--json'], true);
  channel.clear();
  channel.appendLine('codeintel query symbol ' + sym + ' (repo: ' + c.repo + ')');
  channel.appendLine('='.repeat(50));
  if (r.error) {
    channel.appendLine('✗ ' + r.error);
  } else {
    channel.appendLine(renderSymbol(r.data));
  }
  channel.show();
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
  } else {
    const d = r.data;
    channel.appendLine('目标: ' + d.target);
    const nodes = d.nodes || [];
    channel.appendLine('影响节点（' + nodes.length + ' 个）:');
    nodes.forEach(function (n) {
      channel.appendLine('  [' + n.kind + '] ' + n.name + (n.file ? '  ' + n.file + (n.line ? ':' + n.line : '') : ''));
    });
  }
  channel.show();
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

module.exports = { activate, deactivate, renderSymbol, shortID };
