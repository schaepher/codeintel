// codeintel VS Code 扩展：Activity Bar 查询视图 + 调用链 + 时序图。
// 后端复用 codeintel CLI（--json 契约，docs/json-contract.md）——扩展只做
// 输入、execFile、树渲染与文件跳转，不重复实现任何图查询逻辑。
'use strict';

const vscode = require('vscode');
const { execFile } = require('child_process');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

/** @type {vscode.OutputChannel} */
let channel;
/** @type {ResultProvider} */
let provider;
// lastQuery 最近一次查询的符号（「生成调用时序图」按钮用）。
let lastQuery = null;

function activate(context) {
  channel = vscode.window.createOutputChannel('codeintel');
  provider = new ResultProvider();
  context.subscriptions.push(
    channel,
    vscode.window.registerTreeDataProvider('codeintel.results', provider),
    vscode.commands.registerCommand('codeintel.querySymbol', () => querySymbol()),
    vscode.commands.registerCommand('codeintel.querySymbolAt', () => querySymbolAt()), // #215 深化：光标处符号
    vscode.commands.registerCommand('codeintel.impact', () => queryImpact()),
    vscode.commands.registerCommand('codeintel.updateIndex', () => updateIndex()),
    vscode.commands.registerCommand('codeintel.generateSequence', () => generateSequence()),
    vscode.commands.registerCommand('codeintel.refreshResults', () => provider.refresh()),
    vscode.commands.registerCommand('codeintel.openResult', (node) => openNode(node))
  );
  bindHover(context);      // #215 深化：悬停 Go 标识符显示符号信息
  bindAutoUpdate(context); // #234：保存 .go 后自动更新索引
}

function deactivate() {}

// ---- 配置 ----

function cfg() {
  const c = vscode.workspace.getConfiguration('codeintel');
  const raw = c.get('repoPath', '');
  let repo = raw;
  if (!repo && vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders.length) {
    repo = vscode.workspace.workspaceFolders[0].uri.fsPath;
  }
  return { bin: c.get('binaryPath', 'codeintel'), repo: repo || '.', explicitRepo: !!raw };
}

function chainDepth() {
  const n = vscode.workspace.getConfiguration('codeintel').get('callChainDepth', 10);
  return typeof n === 'number' && n > 0 ? n : 10;
}

function maxTreeNodes() {
  const n = vscode.workspace.getConfiguration('codeintel').get('maxTreeNodes', 100);
  return typeof n === 'number' && n > 0 ? n : 100;
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

// ---- #215 深化：Hover 提示 ----

function bindHover(context) {
  if (!vscode.languages || !vscode.languages.registerHoverProvider) return;
  context.subscriptions.push(vscode.languages.registerHoverProvider('go', {
    provideHover: function (doc, pos) {
      const range = doc.getWordRangeAtPosition(pos, /[A-Za-z_][A-Za-z0-9_.]*/);
      if (!range) return null;
      const word = doc.getText(range);
      const c = cfg();
      return runCli(['query', 'symbol', word, '--repo', c.repo, '--json'], true).then(function (r) {
        if (r.error || !r.data || !r.data.name) return null;
        const md = new vscode.MarkdownString();
        md.appendMarkdown(renderHoverMarkdown(r.data));
        return new vscode.Hover(md, range);
      });
    }
  }));
}

// currentWord 光标处标识符（选中区优先，否则 word range）。
function currentWord(ed) {
  const sel = ed.selection;
  if (sel && !sel.isEmpty) return ed.document.getText(sel);
  const range = ed.document.getWordRangeAtPosition(ed.selection.active, /[A-Za-z_][A-Za-z0-9_.]*/);
  return range ? ed.document.getText(range) : '';
}

// ---- CLI 调用 ----

// runCli 执行 codeintel 命令；json=true 时解析 --json 输出，失败返回错误信息。
function runCli(args, json) {
  const c = cfg();
  return new Promise(function (resolve) {
    execFile(c.bin, args, { maxBuffer: 10 * 1024 * 1024 }, function (err, stdout, stderr) {
      if (err) {
        if (/ENOENT|not found/i.test(String(err.message))) {
          resolve({ error: '找不到 codeintel 可执行文件（配置 codeintel.binaryPath；安装见仓库 README）' });
          return;
        }
        resolve({ error: (stderr || err.message || '').trim() || String(err.message) });
        return;
      }
      if (json) {
        try {
          resolve({ data: JSON.parse(stdout) });
        } catch (e) {
          resolve({ error: 'CLI 输出非 JSON：' + String(stdout).slice(0, 500) });
        }
        return;
      }
      resolve({ text: stdout });
    });
  });
}

// ---- 仓库 / 光标符号定位 ----

// detectRepo 定位目标仓库：配置 > 工作区根；根无 go.mod 时遍历第一层
// 含 go.mod 的目录（多命中让用户选择）。
async function detectRepo() {
  const c = cfg();
  const base = c.repo && c.repo !== '.' ? c.repo : firstWorkspaceFolder();
  if (!base) return c.repo || '.';
  if (hasGoMod(base)) return base;
  let entries = [];
  try {
    entries = fs.readdirSync(base, { withFileTypes: true });
  } catch (e) {
    return base;
  }
  const mods = entries.filter(function (e) { return e.isDirectory() && hasGoMod(path.join(base, e.name)); })
    .map(function (e) { return path.join(base, e.name); });
  if (mods.length === 1) return mods[0];
  if (mods.length > 1) {
    const pick = await vscode.window.showQuickPick(mods.map(function (m) {
      return { label: path.basename(m), description: m };
    }), { placeHolder: '工作区含多个 Go module，选择目标仓库' });
    if (pick) return pick.description;
  }
  return base;
}

function firstWorkspaceFolder() {
  if (vscode.workspace && vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders.length) {
    return vscode.workspace.workspaceFolders[0].uri.fsPath;
  }
  return '';
}

function hasGoMod(dir) {
  try {
    return fs.existsSync(path.join(dir, 'go.mod'));
  } catch (e) {
    return false;
  }
}

// findGoModRoot 从 dir 逐层向上找 go.mod，返回 {root, module}（module
// 为 go.mod 里的 module path）。
function findGoModRoot(dir) {
  let cur = dir;
  for (let i = 0; i < 50; i++) {
    const modFile = path.join(cur, 'go.mod');
    if (fs.existsSync(modFile)) {
      let modulePath = '';
      try {
        const m = /^\s*module\s+(\S+)/m.exec(fs.readFileSync(modFile, 'utf8'));
        if (m) modulePath = m[1];
      } catch (e) { /* ignore */ }
      return { root: cur, module: modulePath };
    }
    const parent = path.dirname(cur);
    if (parent === cur) break;
    cur = parent;
  }
  return null;
}

// queryRepos 待查询仓库列表：显式配置 repoPath 时只查指定仓库；否则
// 依次尝试工作区根 + 第一层含 go.mod 的目录（找到结果即停止）。
function queryRepos(c) {
  if (c.explicitRepo && c.repo) return [c.repo];
  const base = c.repo && c.repo !== '.' ? c.repo : firstWorkspaceFolder();
  const out = [];
  const add = function (p) { if (p && out.indexOf(p) < 0) out.push(p); };
  if (!base) {
    add(c.repo || '.');
    return out;
  }
  if (hasGoMod(base)) add(base);
  try {
    fs.readdirSync(base, { withFileTypes: true })
      .filter(function (e) { return e.isDirectory() && hasGoMod(path.join(base, e.name)); })
      .forEach(function (e) { add(path.join(base, e.name)); });
  } catch (e) { /* ignore */ }
  if (out.length === 0) add(base);
  return out;
}

// focusResults 把活动视图切到 Activity Bar 查询结果（侧栏）；失败静默
// （命令未注册/无视图时不阻塞查询）。
function focusResults() {
  try {
    vscode.commands.executeCommand('codeintel.results.focus');
  } catch (e) { /* ignore */ }
}

// funcDeclAt 读取文件并从 1-based line 向上找最近的 func 声明（name/receiver）。
function funcDeclAt(file, line) {
  let text = '';
  try {
    text = fs.readFileSync(file, 'utf8');
  } catch (e) {
    return null;
  }
  return funcDeclInText(text, line);
}

// funcDeclInText 文本 + 1-based 行 → 最近的 func 声明（name/receiver）。
function funcDeclInText(text, line) {
  const lines = String(text).split('\n');
  const start = Math.min(Math.max(line - 1, 0), lines.length - 1);
  for (let i = start; i >= 0 && i >= start - 400; i--) {
    const m = /^func\s+(?:\(\s*\w+\s+\*?([A-Za-z0-9_\[\]]+)\s*\)\s+)?([A-Za-z0-9_]+)/.exec(lines[i]);
    if (m) return { recv: m[1] || '', name: m[2] };
  }
  return null;
}

// normalizeDefinition 归一 Go 定义提供器结果（Location / LocationLink）
// 为 {file, line}（1-based）。
function normalizeDefinition(d) {
  if (!d) return null;
  const uri = d.targetUri || d.uri;
  const range = d.targetSelectionRange || d.targetRange || d.range;
  if (!uri || !range || !range.start) return null;
  return { file: uri.fsPath || String(uri), line: range.start.line + 1 };
}

// definitionLocations 通过 VS Code Go 定义提供器取真实定义位置（支持
// Location 与 LocationLink），去重后返回 [{file,line}]。
async function definitionLocations(ed) {
  let defs = [];
  try {
    defs = await vscode.commands.executeCommand('vscode.executeDefinitionProvider',
      ed.document.uri, ed.selection.active);
  } catch (e) {
    return null;
  }
  const out = [];
  const seen = {};
  (defs || []).forEach(function (d) {
    const loc = normalizeDefinition(d);
    if (!loc) return;
    const k = loc.file + ':' + loc.line;
    if (seen[k]) return;
    seen[k] = true;
    out.push(loc);
  });
  return out;
}

// canonicalIDForDefinition 定义文件 + 行 → canonical ID：从**定义文件**
// 最近的 go.mod 取 module path（跨包调用用定义的包，而非调用点），
// 定义行的 func 声明给出 receiver 与函数名。
function canonicalIDForDefinition(file, line) {
  const mod = findGoModRoot(path.dirname(file));
  if (!mod || !mod.module) return null;
  const rel = path.relative(mod.root, path.dirname(file)).split(path.sep).filter(Boolean).join('/');
  const pkgPath = rel ? mod.module + '/' + rel : mod.module;
  const decl = funcDeclAt(file, line);
  if (!decl) return null;
  const name = decl.recv ? '(' + decl.recv + ').' + decl.name : decl.name;
  return { id: 'symbol:go:' + pkgPath + ':' + name, name: name, repo: mod.root, file: file, line: line };
}

// cursorTargetFromDefinition 光标处符号 → 真实定义位置 → canonical ID。
// 多定义时不猜测（返回 error=multiple 由调用方提示）。
async function cursorTargetFromDefinition(ed) {
  const defs = await definitionLocations(ed);
  if (!defs) return { error: 'no-definition' };
  if (defs.length === 0) return { error: 'no-definition' };
  if (defs.length > 1) return { error: 'multiple', count: defs.length };
  const t = canonicalIDForDefinition(defs[0].file, defs[0].line);
  if (!t) return { error: 'no-module' };
  return t;
}

// enclosingFuncDecl 光标所在（或最近上方）的 func 声明：name/receiver。
function enclosingFuncDecl(doc, line) {
  for (let i = line; i >= 0 && i >= line - 400; i--) {
    const text = doc.lineAt(i).text;
    const m = /^func\s+(?:\(\s*\w+\s+\*?([A-Za-z0-9_\[\]]+)\s*\)\s+)?([A-Za-z0-9_]+)/.exec(text);
    if (m) return { recv: m[1] || '', name: m[2] };
  }
  return null;
}

// receiverOfMethodInPackage 在光标文件所在目录扫描 .go 文件，找到唯一
// 具有该方法名的方法 receiver 类型（定义处/同包调用点都能拼出 canonical
// ID；多命中返回 null，退回包级函数名）。
function receiverOfMethodInPackage(fileName, method) {
  const dir = path.dirname(fileName);
  let files = [];
  try {
    files = fs.readdirSync(dir).filter(function (f) { return f.endsWith('.go') && !f.endsWith('_test.go'); });
  } catch (e) {
    return null;
  }
  const found = new Set();
  const re = /^func\s+\(\s*\w+\s+\*?([A-Za-z0-9_\[\]]+)\s*\)\s+([A-Za-z0-9_]+)/;
  for (const f of files) {
    let text = '';
    try {
      text = fs.readFileSync(path.join(dir, f), 'utf8');
    } catch (e) {
      continue;
    }
    text.split('\n').forEach(function (l) {
      const m = re.exec(l);
      if (m && m[2] === method) found.add(m[1]);
    });
  }
  return found.size === 1 ? Array.from(found)[0] : null;
}

// ---- 命令实现 ----

async function querySymbol() {
  const sym = await vscode.window.showInputBox({
    prompt: '符号名或 canonical ID',
    placeHolder: '如 (Manager).Run 或 symbol:go:...:main'
  });
  if (!sym) return;
  const c = cfg();
  const repos = queryRepos(c);
  // 多仓库：依次查询工作区第一层 Go module，找到结果即停止
  for (const repo of repos) {
    const r = await runCli(['query', 'symbol', sym, '--repo', repo, '--json'], true);
    if (!r.error && r.data && r.data.name) {
      await queryAndShow(sym, { bin: c.bin, repo: repo });
      return;
    }
  }
  provider.setError('✗ 未在 ' + repos.join(' / ') + ' 找到符号 ' + sym);
}

// querySymbolAt 光标处符号直接查询（Hover「查看详情」/ 命令入口）：
// 经 Go 定义提供器取真实定义 → canonical ID → 精确查询。
async function querySymbolAt() {
  const ed = vscode.window.activeTextEditor;
  if (!ed) return;
  const t = await cursorTargetFromDefinition(ed);
  if (!t || t.error) {
    const msg = t && t.error === 'multiple'
      ? 'codeintel: 符号有多个定义，无法确定唯一 canonical ID（请用「查询符号」输入完整 ID）'
      : 'codeintel: 无法从定义定位符号（需要 Go 定义与 go.mod）';
    vscode.window.showWarningMessage(msg);
    return;
  }
  await queryAndShow(t.id, { bin: cfg().bin, repo: t.repo });
}

// queryAndShow 查询符号并展示（Activity Bar 树 + Output 文本）——一次
// 查询完整调用深度，在内存中组装调用树（不再逐层启动 CLI）。
async function queryAndShow(sym, c) {
  provider.setLoading('搜索中…');
  focusResults();
  let r = await runCli(['query', 'symbol', sym, '--repo', c.repo, '--json'], true);
  // #236：多匹配（重名符号）→ QuickPick 候选 → 用 canonical ID 重查
  if (r.error) {
    const cands = parseCandidates(r.error);
    if (cands.length > 0) {
      const pick = await vscode.window.showQuickPick(cands.map(function (id) {
        return { label: shortID(id), description: id };
      }), { placeHolder: '多个匹配，选择符号（' + cands.length + ' 个）' });
      if (!pick) { provider.setError('已取消'); return; }
      r = await runCli(['query', 'symbol', pick.description, '--repo', c.repo, '--json'], true);
    }
  }
  if (r.error) {
    provider.setError('✗ ' + r.error);
    channel.clear();
    channel.appendLine('codeintel query symbol ' + sym + ' (repo: ' + c.repo + ')');
    channel.appendLine('✗ ' + r.error);
    return; // 不自动弹出 Output 面板（错误仅留在面板/树）
  }
  const d = r.data;
  lastQuery = { id: d.id, name: d.name, repo: c.repo };
  const depth = chainDepth();
  const both = await Promise.all([
    runCli(['query', 'callers', d.id, '--repo', c.repo, '--depth', String(depth), '--json'], true),
    runCli(['query', 'callees', d.id, '--repo', c.repo, '--depth', String(depth), '--json'], true)
  ]);
  const callerRows = rowsOf(both[0]);
  const calleeRows = rowsOf(both[1]);
  const built = await buildResult(d, callerRows, calleeRows, c, depth);
  provider.setRoots(built.roots);
  channel.clear();
  channel.appendLine('codeintel query symbol ' + sym + ' (repo: ' + c.repo + ')');
  channel.appendLine('='.repeat(50));
  channel.appendLine(renderSymbol(d));
  channel.appendLine('');
  channel.appendLine('上层调用链:');
  channel.appendLine(renderTreeText(built.upstream) || '  （无）');
  channel.appendLine('');
  channel.appendLine('下游调用链:');
  channel.appendLine(renderTreeText(built.downstream) || '  （无）');
}

// rowsOf 取 CLI --json 结果的 rows；旧版缺 source/target 时由 fallback 处理。
function rowsOf(r) {
  return (r && r.data && Array.isArray(r.data.rows)) ? r.data.rows : [];
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
    provider.setError('✗ ' + r.error);
    return; // 不自动弹出 Output 面板
  }
  const d = r.data;
  channel.appendLine('目标: ' + d.target);
  const nodes = d.nodes || [];
  channel.appendLine('影响节点（' + nodes.length + ' 个）:');
  nodes.forEach(function (n) {
    channel.appendLine('  [' + n.kind + '] ' + n.name + (n.file ? '  ' + n.file + (n.line ? ':' + n.line : '') : ''));
  });
  const impactRoots = [group('影响节点（' + nodes.length + '）', nodes.map(function (n) {
    return symbolNode({ name: n.name, kind: n.kind, file: n.file, line: n.line, id: n.id }, []);
  }))];
  assignRepo(impactRoots, c.repo);
  provider.setRoots(impactRoots);
}

async function updateIndex() {
  const c = cfg();
  channel.clear();
  channel.appendLine('codeintel update --repo ' + c.repo);
  channel.appendLine('='.repeat(50));
  const r = await runCli(['update', '--repo', c.repo], false);
  if (r.error) {
    channel.appendLine('✗ ' + r.error);
    provider.setError('✗ ' + r.error);
  } else {
    channel.appendLine(r.text);
  }
  // 不自动弹出 Output 面板（changelog：更新索引不再抢焦点）
}

// ---- 调用树组装 ----

// buildResult 由符号详情 + 扁平调用边组装结果树：符号详情 / 调用者 /
// 被调用者 / 上层调用链 / 下游调用链。调用边缺 source/target（旧 CLI）
// 时回退逐层查询。
async function buildResult(d, callerRows, calleeRows, c, depth) {
  const cap = maxTreeNodes();
  let upstream = buildChain(callerRows, d.id, 'up', depth, cap);
  let downstream = buildChain(calleeRows, d.id, 'down', depth, cap);
  let callersList = upstream;
  let calleesList = downstream;
  if (callerRows.length && !callerRows[0].source) {
    upstream = await chainFallback(d.id, c.repo, 'callers', depth, cap);
    callersList = upstream;
  }
  if (calleeRows.length && !calleeRows[0].source) {
    downstream = await chainFallback(d.id, c.repo, 'callees', depth, cap);
    calleesList = downstream;
  }
  const sym = symbolNode(d, [
    textNode('位置: ' + (d.file || '?') + (d.line ? ':' + d.line : '')),
    d.signature ? textNode('签名: ' + d.signature) : null,
    d.doc ? textNode('文档: ' + String(d.doc).split('\n')[0]) : null
  ].filter(Boolean));
  const roots = [
    sym,
    group('调用者（' + callersList.length + '）', callersList.length ? callersList : [emptyNode()]),
    group('被调用者（' + calleesList.length + '）', calleesList.length ? calleesList : [emptyNode()]),
    group('上层调用链', upstream.length ? upstream : [emptyNode()]),
    group('下游调用链', downstream.length ? downstream : [emptyNode()])
  ];
  // 仓库保持：所有结果节点记住本次查询的仓库（直接跳转/补查均用它）
  assignRepo(roots, c.repo);
  return { roots: roots, upstream: upstream, downstream: downstream };
}

// assignRepo 递归为结果节点写入查询仓库（openAt/resolveAndJump 用它）。
function assignRepo(nodes, repo) {
  (nodes || []).forEach(function (n) {
    n.repo = repo;
    if (n.children && n.children.length) assignRepo(n.children, repo);
  });
}

// buildChain 由扁平边 rows 组装调用树。dir=up：target→source（调用者树）；
// dir=down：source→target（被调用者树）。深度 ≤ maxDepth，节点数 ≤ cap，
// 路径访问集防环。
function buildChain(rows, rootId, dir, maxDepth, cap) {
  const adj = new Map();
  rows.forEach(function (r) {
    if (!r.source || !r.target) return;
    const from = dir === 'up' ? r.target : r.source;
    const to = dir === 'up' ? r.source : r.target;
    if (!adj.has(from)) adj.set(from, []);
    adj.get(from).push({ edge: r, child: to });
  });
  const state = { count: 0 };
  const visited = new Set([rootId]);
  return expandChain(rootId, 1);
  function expandChain(id, level) {
    if (level > maxDepth || state.count >= cap) return [];
    const out = [];
    const list = adj.get(id) || [];
    for (const it of list) {
      if (state.count >= cap) break;
      if (visited.has(it.child)) continue;
      visited.add(it.child);
      state.count++;
      const node = symbolNode(rowNode(it.edge, it.child), expandChain(it.child, level + 1));
      out.push(node);
    }
    return out;
  }
}

// chainFallback 旧 CLI（rows 缺 source/target）的逐层查询兜底：BFS 每层
// query callers/callees --depth 1，节点数 ≤ cap。
async function chainFallback(symId, repo, which, maxDepth, cap) {
  const state = { count: 0 };
  const visited = new Set([symId]);
  async function expand(id, level) {
    if (level > maxDepth || state.count >= cap) return [];
    const r = await runCli(['query', which, id, '--repo', repo, '--depth', '1', '--json'], true);
    const rows = rowsOf(r);
    const out = [];
    for (const row of rows) {
      const cid = row.id;
      if (!cid || visited.has(cid)) continue;
      visited.add(cid);
      state.count++;
      out.push(symbolNode(rowNode(row, cid), await expand(cid, level + 1)));
      if (state.count >= cap) break;
    }
    return out;
  }
  return expand(symId, 1);
}

// rowNode 由边行构造节点（端点 id 已解析给出）。
function rowNode(row, childId) {
  return {
    id: childId || row.id,
    name: row.name || shortID(childId || row.id || ''),
    kind: row.kind || '',
    file: row.file || '',
    line: row.line || 0,
    signature: row.signature || ''
  };
}

// ---- 树节点 ----

function symbolNode(d, children) {
  return {
    label: d.name || shortID(d.id),
    description: d.kind || '',
    tooltip: tooltipFor(d),
    file: d.file || '',
    line: d.line || 0,
    symbolId: d.id || '',
    children: children || [],
    jump: !!(d.file || d.id)
  };
}

function group(label, children) {
  return { label: label, isGroup: true, children: children || [], jump: false };
}

function emptyNode() {
  return { label: '无', isGroup: false, children: [], jump: false };
}

function textNode(label) {
  return { label: label, isGroup: false, children: [], jump: false };
}

function tooltipFor(d) {
  const md = new vscode.MarkdownString();
  md.appendMarkdown('**' + (d.name || '') + '** `' + (d.kind || '') + '`');
  if (d.id) md.appendMarkdown('\n\n`' + d.id + '`');
  if (d.signature) md.appendMarkdown('\n\n`' + d.signature + '`');
  if (d.file) md.appendMarkdown('\n\n' + d.file + (d.line ? ':' + d.line : ''));
  return md;
}

// renderTreeText 调用树 → 缩进文本（Output Channel 同步展示用，复用侧栏
// 结果，不额外调用 CLI）。
function renderTreeText(nodes) {
  const lines = [];
  (function walk(list, depth) {
    list.forEach(function (n) {
      lines.push('  '.repeat(depth) + '- ' + n.label + (n.description ? ' [' + n.description + ']' : '') +
        (n.file ? '  ' + n.file + (n.line ? ':' + n.line : '') : ''));
      if (n.children && n.children.length) walk(n.children, depth + 1);
    });
  })(nodes || [], 0);
  return lines.join('\n');
}

// ---- 查询结果树 Provider ----

class ResultProvider {
  constructor() {
    this._onDidChange = new vscode.EventEmitter();
    this.onDidChangeTreeData = this._onDidChange.event;
    this.roots = [];
  }
  setLoading(label) {
    this.roots = [{ label: label, children: [], jump: false, isGroup: false }];
    this._onDidChange.fire();
  }
  setError(msg) {
    this.roots = [{ label: msg, children: [], jump: false, isGroup: false }];
    this._onDidChange.fire();
  }
  setRoots(roots) {
    this.roots = roots || [];
    this._onDidChange.fire();
  }
  refresh() {
    this._onDidChange.fire();
  }
  getChildren(node) {
    if (!node) return this.roots;
    return node.children || [];
  }
  getTreeItem(node) {
    const collapsible = (node.children && node.children.length)
      ? vscode.TreeItemCollapsibleState.Expanded
      : vscode.TreeItemCollapsibleState.None;
    const item = new vscode.TreeItem(node.label || '', collapsible);
    item.description = node.description || '';
    if (node.tooltip) item.tooltip = node.tooltip;
    if (node.isGroup) {
      item.contextValue = 'codeintel.group';
      item.iconPath = new vscode.ThemeIcon('list-tree');
    } else if (node.jump) {
      item.contextValue = 'codeintel.symbol';
      item.iconPath = new vscode.ThemeIcon('symbol-function');
      item.command = { command: 'codeintel.openResult', title: '跳转定义', arguments: [node] };
    }
    return item;
  }
}

// openNode 点击树节点跳转定义：优先用节点自带 file/line（新 CLI 富化），
// 否则按 canonical ID 解析位置（旧输出/无位置节点）。
async function openNode(node) {
  if (!node || !node.jump) return;
  if (node.file) {
    await openAt(node.file, node.line, node.repo);
    return;
  }
  if (node.symbolId) await resolveAndJump(node.symbolId, node.repo);
}

// ---- #236 跳到定义 ----

async function openAt(file, line, repo) {
  if (!file) return;
  const c = cfg();
  const base = repo || c.repo;
  const abs = path.isAbsolute(file) ? file : path.join(base, file);
  try {
    const doc = await vscode.workspace.openTextDocument(abs);
    const editor = await vscode.window.showTextDocument(doc);
    if (line) {
      const r = new vscode.Range(line - 1, 0, line - 1, 0);
      editor.revealRange(r);
      editor.selection = new vscode.Selection(r.start, r.start);
    }
  } catch (e) {
    vscode.window.showWarningMessage('codeintel: 无法打开 ' + abs + '（' + e.message + '）');
  }
}

// resolveAndJump 无位置的节点：按 canonical ID 二次解析位置再跳转
// （用节点记录的查询仓库，而非全局配置）。
async function resolveAndJump(id, repo) {
  const c = cfg();
  const base = repo || c.repo;
  const r = await runCli(['query', 'symbol', id, '--repo', base, '--json'], true);
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

// ---- 生成调用时序图 ----

// sequenceFileName 时序图文件名：符号短名 + canonical ID 哈希（避免同名
// 符号互相覆盖）。
function sequenceFileName(id, name) {
  const safe = String(name || 'sequence').replace(/[^A-Za-z0-9_.-]+/g, '_');
  const h = crypto.createHash('sha1').update(String(id || '')).digest('hex').slice(0, 8);
  return safe + '-' + h;
}

// isPNG 判定字节是否为 PNG（R17：区分图片预览与文本打开）。
function isPNG(buf) {
  return !!(buf && buf.length > 8 && buf[0] === 0x89 && buf[1] === 0x50 &&
    buf[2] === 0x4e && buf[3] === 0x47);
}

// seqArtifactOpenMode 时序图产物打开方式：'image'（PNG 预览）| 'text'（编辑器）。
// 避免把 PNG 当文本打开（乱码），也避免把 mermaid 文本当图片。
function seqArtifactOpenMode(buf) {
  return isPNG(buf) ? 'image' : 'text';
}

// pumlSidecarPath 与 PNG 同目录的可编辑 PlantUML 源文件路径（CLI 侧写入）。
function pumlSidecarPath(pngPath) {
  return String(pngPath).replace(/\.png$/i, '') + '.puml';
}

// openSequenceArtifact 按产物类型打开：PNG → 图片预览（vscode.open，绝不
// 交给文本编辑器）+ 可编辑的 PlantUML 源（文本编辑器）；mermaid 回退 →
// 改名为 .mmd 后文本编辑器打开。返回实际打开/保留的主产物路径。
async function openSequenceArtifact(dir, safe, pngPath) {
  const buf = fs.readFileSync(pngPath);
  if (seqArtifactOpenMode(buf) === 'image') {
    await vscode.commands.executeCommand('vscode.open', vscode.Uri.file(pngPath), { preview: true });
    const pumlPath = pumlSidecarPath(pngPath);
    if (fs.existsSync(pumlPath)) {
      const pdoc = await vscode.workspace.openTextDocument(pumlPath);
      await vscode.window.showTextDocument(pdoc, { preview: true });
    }
    return pngPath;
  }
  const outPath = path.join(dir, safe + '.mmd');
  fs.renameSync(pngPath, outPath);
  const doc = await vscode.workspace.openTextDocument(outPath);
  await vscode.window.showTextDocument(doc, { preview: true });
  return outPath;
}

// generateSequence Activity Bar 标题按钮：对最近一次查询的符号执行
// `query sequence --code --format plantuml`，产物写到
// <repo>/.codeintel/sequence/ 后自动打开。CLI 可渲染时输出 PNG；PlantUML
// 不可用（回退 Mermaid 文本）时改名为 .mmd 再打开。用户最后一次成功
// 查询的**仓库**优先（多仓库场景）。
async function generateSequence() {
  if (!lastQuery) {
    vscode.window.showWarningMessage('codeintel: 请先查询一个符号，再生成调用时序图');
    return;
  }
  const c = cfg();
  const repo = lastQuery.repo || c.repo;
  const dir = path.join(repo, '.codeintel', 'sequence');
  try {
    fs.mkdirSync(dir, { recursive: true });
  } catch (e) {
    vscode.window.showErrorMessage('codeintel: 无法创建 ' + dir + '（' + e.message + '）');
    return;
  }
  const safe = sequenceFileName(lastQuery.id, lastQuery.name);
  const pngPath = path.join(dir, safe + '.png');
  channel.clear();
  channel.appendLine('codeintel query sequence --code ' + lastQuery.id + ' (repo: ' + repo + ')');
  channel.appendLine('='.repeat(50));
  const r = await runCli(['query', 'sequence', '--code', lastQuery.id, '--repo', repo,
    '--format', 'plantuml', '--out', pngPath], false);
  if (r.error) {
    channel.appendLine('✗ ' + r.error);
    vscode.window.showErrorMessage('codeintel: 生成时序图失败（' + r.error + '）');
    return;
  }
  channel.appendLine(r.text || '');
  // R17：按产物类型打开（PNG → 图片预览 + PlantUML 源；文本 → .mmd 编辑器）
  let outPath;
  try {
    outPath = await openSequenceArtifact(dir, safe, pngPath);
  } catch (e) {
    vscode.window.showErrorMessage('codeintel: 打开时序图产物失败（' + e.message + '）');
    return;
  }
  channel.appendLine('已打开 ' + outPath);
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
      lines.push('  ' + shortID(f.id) + '  (conf=' + Number(f.confidence || 0).toFixed(2) + ')');
    });
  }
  if (d.callees && d.callees.length) {
    lines.push('');
    lines.push('被调用者（' + d.callees.length + '）:');
    d.callees.forEach(function (f) {
      lines.push('  ' + shortID(f.id) + '  (conf=' + Number(f.confidence || 0).toFixed(2) + ')');
    });
  }
  return lines.join('\n');
}

// shortID 压缩 canonical ID 便于阅读（symbol:go:<pkg>:<name> → <name>）。
function shortID(id) {
  const i = String(id).lastIndexOf(':');
  return i >= 0 ? String(id).slice(i + 1) : String(id);
}

// renderHoverMarkdown Hover 内容组装（纯函数可单测）：名称/kind/签名/
// 位置/调用者被调用者计数 + 「查看详情」命令入口。
function renderHoverMarkdown(d) {
  const parts = ['**' + d.name + '** `' + (d.kind || '') + '`'];
  if (d.signature) parts.push('\n\n`' + d.signature + '`');
  if (d.file) parts.push('\n\n' + d.file + (d.line ? ':' + d.line : ''));
  const extra = [];
  if (d.callers && d.callers.length) extra.push('调用者 ' + d.callers.length);
  if (d.callees && d.callees.length) extra.push('被调用者 ' + d.callees.length);
  if (extra.length) parts.push('\n\n' + extra.join(' · '));
  parts.push('\n\n[查看详情](command:codeintel.querySymbolAt)');
  return parts.join('');
}

module.exports = {
  activate, deactivate, renderSymbol, renderHoverMarkdown, shortID,
  shouldAutoUpdateFileName, parseCandidates, openAt,
  queryRepos, normalizeDefinition, canonicalIDForDefinition, funcDeclInText, sequenceFileName,
  isPNG, seqArtifactOpenMode, pumlSidecarPath, openSequenceArtifact
};
