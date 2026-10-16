#!/usr/bin/env node
// selfcheck-vscode-extension.cjs：VS Code 扩展纯逻辑自检（无需真实 VS Code）。
// 覆盖：定义定位（Location/LocationLink → canonical ID）、多仓库查询顺序
// （配置优先）、跳转（节点仓库保持）、时序图文件名哈希。
//
// 用法：node scripts/selfcheck-vscode-extension.cjs
'use strict';

const Module = require('module');
const fs = require('fs');
const os = require('os');
const path = require('path');
const assert = require('assert');

// ---- vscode mock（extension.js 顶层 require('vscode')）----

let configuredRepo = '';
const opened = [];
const execCommands = [];
const fakeVscode = {
  Uri: { file: function (p) { return { fsPath: p }; } },
  workspace: {
    workspaceFolders: [],
    getConfiguration: function () {
      return { get: function (k, d) { return k === 'repoPath' ? configuredRepo : d; } };
    },
    openTextDocument: async function (p) { opened.push(p); return { fileName: p, uri: { fsPath: p } }; },
    onDidSaveTextDocument: function () { return { dispose: function () {} }; }
  },
  window: {
    createOutputChannel: function () { return { appendLine: function () {}, clear: function () {}, show: function () {}, dispose: function () {} }; },
    registerTreeDataProvider: function () { return { dispose: function () {} }; },
    showTextDocument: async function () { return { revealRange: function () {}, selection: null }; },
    showWarningMessage: function () {}, showErrorMessage: function () {},
    showQuickPick: async function () { return undefined; },
    showInputBox: async function () { return undefined; }
  },
  commands: {
    registerCommand: function () { return { dispose: function () {} }; },
    executeCommand: async function (cmd) {
      execCommands.push({ cmd: cmd, args: Array.prototype.slice.call(arguments, 1) });
      return [];
    }
  },
  languages: { registerHoverProvider: function () { return { dispose: function () {} }; } },
  EventEmitter: class { constructor() { this.event = function () { return { dispose: function () {} }; }; } fire() {} },
  TreeItem: class { constructor(label, state) { this.label = label; this.state = state; } },
  TreeItemCollapsibleState: { None: 0, Expanded: 1 },
  MarkdownString: class { appendMarkdown() {} },
  ThemeIcon: class { constructor(id) { this.id = id; } },
  Range: class { constructor(a, b, c, d) { this.start = { line: a, character: b }; this.end = { line: c, character: d }; } },
  Selection: class { constructor(a, b) { this.start = a; this.end = b; } },
  Hover: class {}
};

const origLoad = Module._load;
Module._load = function (request, parent, isMain) {
  if (request === 'vscode') return fakeVscode;
  return origLoad.apply(this, arguments);
};

const ext = require('../vscode-extension/extension.js');

// ---- helpers ----

let failures = 0;
const asyncChecks = [];
function check(name, fn) {
  // 异步用例延迟到同步用例之后**串行**执行（否则共享的 opened/execCommands
  // 会被并发的用例互相清空——自检必须确定性）。
  if (fn && fn.constructor && fn.constructor.name === 'AsyncFunction') {
    asyncChecks.push({ name: name, fn: fn });
    return;
  }
  try {
    fn();
    console.log('  ✓ ' + name);
  } catch (e) {
    failures++;
    console.error('  ✗ ' + name + '：' + e.message);
  }
}

function tmpdir() {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'codeintel-ext-selfcheck-'));
}

function write(p, content) {
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, content);
}

console.log('selfcheck-vscode-extension');

// 1) 定义定位：Location / LocationLink → canonical ID
check('normalizeDefinition 支持 Location 与 LocationLink', function () {
  const loc = ext.normalizeDefinition({ uri: { fsPath: '/a/b.go' }, range: { start: { line: 4 } } });
  assert.deepStrictEqual(loc, { file: '/a/b.go', line: 5 });
  const link = ext.normalizeDefinition({ targetUri: { fsPath: '/x/y.go' }, targetSelectionRange: { start: { line: 9 } } });
  assert.deepStrictEqual(link, { file: '/x/y.go', line: 10 });
  assert.strictEqual(ext.normalizeDefinition(null), null);
});

// 2) 定义位置 → canonical ID（含 receiver 与包路径）
const repoRoot = tmpdir();
write(path.join(repoRoot, 'go.mod'), 'module example.com/m\n\ngo 1.21\n');
const svcFile = path.join(repoRoot, 'svc/svc.go');
write(svcFile, 'package svc\n\ntype Svc struct{}\n\nfunc (s *Svc) Run() {}\n\nfunc Helper() {}\n');
check('canonicalIDForDefinition 方法带 receiver', function () {
  const t = ext.canonicalIDForDefinition(svcFile, 5);
  assert.strictEqual(t.id, 'symbol:go:example.com/m/svc:(Svc).Run');
  assert.strictEqual(t.repo, repoRoot);
});
check('canonicalIDForDefinition 函数无 receiver', function () {
  const t = ext.canonicalIDForDefinition(svcFile, 7);
  assert.strictEqual(t.id, 'symbol:go:example.com/m/svc:Helper');
});

// 3) 多仓库查询顺序（配置优先 / 第一层 module）
const ws = tmpdir();
write(path.join(ws, 'go.mod'), 'module example.com/root\n');
write(path.join(ws, 'mod1/go.mod'), 'module example.com/m1\n');
write(path.join(ws, 'mod2/go.mod'), 'module example.com/m2\n');
write(path.join(ws, 'notmod/readme.md'), 'x');
fakeVscode.workspace.workspaceFolders = [{ uri: { fsPath: ws } }];
check('queryRepos 未配置时列出工作区第一层 module', function () {
  configuredRepo = '';
  const repos = ext.queryRepos({ explicitRepo: false, repo: ws });
  assert.ok(repos.indexOf(ws) >= 0, '应含工作区根');
  assert.ok(repos.indexOf(path.join(ws, 'mod1')) >= 0, '应含 mod1');
  assert.ok(repos.indexOf(path.join(ws, 'mod2')) >= 0, '应含 mod2');
  assert.ok(repos.indexOf(path.join(ws, 'notmod')) < 0, '不含非 module 目录');
});
check('queryRepos 显式配置时只查指定仓库', function () {
  configuredRepo = path.join(ws, 'mod2');
  const repos = ext.queryRepos({ explicitRepo: true, repo: configuredRepo });
  assert.deepStrictEqual(repos, [configuredRepo]);
  configuredRepo = '';
});

// 4) 时序图文件名：哈希避免同名覆盖
check('sequenceFileName 含 canonical ID 哈希且同名不同 ID 不冲突', function () {
  const a = ext.sequenceFileName('symbol:go:example.com/m/svc:(Svc).Run', '(Svc).Run');
  const b = ext.sequenceFileName('symbol:go:example.com/m/other:(Svc).Run', '(Svc).Run');
  assert.notStrictEqual(a, b);
  assert.ok(/^_Svc_\.Run-[0-9a-f]{8}$/.test(a), a);
});

// 5) 跳转使用节点仓库（非全局配置）
check('openAt 使用节点仓库解析相对路径', async function () {
  configuredRepo = '';
  opened.length = 0;
  await ext.openAt('svc/svc.go', 5, repoRoot);
  assert.strictEqual(opened[0], path.join(repoRoot, 'svc/svc.go'));
});

// 6) R17：时序图产物打开方式（PNG 预览 vs 文本）
check('seqArtifactOpenMode：PNG → image，mermaid 文本 → text', function () {
  const png = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00]);
  assert.strictEqual(ext.seqArtifactOpenMode(png), 'image');
  assert.strictEqual(ext.isPNG(png), true);
  assert.strictEqual(ext.seqArtifactOpenMode(Buffer.from('sequenceDiagram\n  A->>B: x')), 'text');
  assert.strictEqual(ext.isPNG(Buffer.from('x')), false);
});
check('pumlSidecarPath：.png → .puml', function () {
  assert.strictEqual(ext.pumlSidecarPath('/x/seq.png'), '/x/seq.puml');
  assert.strictEqual(ext.pumlSidecarPath('/x/seq'), '/x/seq.puml');
});
check('openSequenceArtifact：PNG 走图片预览 + PlantUML 文本', async function () {
  const dir = tmpdir();
  const png = path.join(dir, 'seq.png');
  fs.writeFileSync(png, Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00]));
  fs.writeFileSync(path.join(dir, 'seq.puml'), '@startuml\nA->B\n@enduml\n');
  execCommands.length = 0;
  opened.length = 0;
  const out = await ext.openSequenceArtifact(dir, 'seq', png);
  assert.strictEqual(out, png);
  assert.ok(execCommands.some(function (c) {
    return c.cmd === 'vscode.open' && c.args[0] && c.args[0].fsPath === png;
  }), 'PNG 应走 vscode.open 图片预览（非文本编辑器）');
  assert.ok(opened.some(function (p) { return /seq\.puml$/.test(p); }), 'PlantUML 源应在文本编辑器打开');
});
check('openSequenceArtifact：mermaid 文本改 .mmd 文本打开', async function () {
  const dir = tmpdir();
  const f = path.join(dir, 'seq.png');
  fs.writeFileSync(f, 'sequenceDiagram\n  A->>B: x\n');
  execCommands.length = 0;
  opened.length = 0;
  const out = await ext.openSequenceArtifact(dir, 'seq', f);
  assert.ok(/seq\.mmd$/.test(out), out);
  assert.ok(opened.some(function (p) { return /seq\.mmd$/.test(p); }), '文本产物应走文本编辑器');
  assert.strictEqual(execCommands.length, 0, '文本产物不应走 vscode.open 图片预览');
});

Promise.resolve()
  .then(async function () {
    for (const c of asyncChecks) {
      try {
        await c.fn();
        console.log('  ✓ ' + c.name);
      } catch (e) {
        failures++;
        console.error('  ✗ ' + c.name + '：' + e.message);
      }
    }
  })
  .then(function () {
    if (failures > 0) {
      console.error('selfcheck FAILED：' + failures + ' 项');
      process.exit(1);
    }
    console.log('selfcheck OK');
  });
