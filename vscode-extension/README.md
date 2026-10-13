# codeintel — VS Code 扩展

在编辑器内直接查询 Go 代码库智能索引：Activity Bar「查询结果」树视图、
符号详情、调用者/被调用者、上下层调用链、调用时序图、改动影响预判、
增量更新索引。**后端复用 codeintel CLI**（--json 契约，
docs/json-contract.md）——扩展只做输入、execFile、树渲染与文件跳转。

## 前置

1. 安装 codeintel CLI（见仓库根 README「安装」）
2. 目标仓库已构建索引：`codeintel init --repo <仓库>`

## 安装（开发模式）

```bash
cd vscode-extension
code --install-extension .   # 或 F5 启动扩展开发宿主
```

## 使用

| 命令 | 作用 |
|---|---|
| `codeintel: 查询符号` | 输入符号名 → 详情 + 调用者/被调用者 + 上下层调用链（多仓库依次查询） |
| `codeintel: 查询光标处符号` | 经 Go 定义提供器取真实定义 → canonical ID → 精确查询 |
| `codeintel: 影响分析` | 输入符号名 → 改前影响节点列表 |
| `codeintel: 增量更新索引` | 增量重建（改了代码先更新再查询） |
| `codeintel: 生成调用时序图` | 对最近一次查询的符号生成 PlantUML（Activity Bar 标题按钮） |

### 查询结果视图（Activity Bar）

- 查询开始显示「搜索中…」并自动切到 Activity Bar 结果视图（不自动弹出 Output）。
- 光标符号查询经 **VS Code Go 定义提供器**（支持 Location / LocationLink）取真实定义，
  从**定义文件**最近的 go.mod 拼 canonical ID（跨包调用用定义的包）；多定义时不
  猜测候选，提示改用「查询符号」输入完整 ID。
- **多仓库**：未显式配置 `repoPath` 时，输入符号后依次查询工作区第一层 Go module，
  找到结果即停止；显式配置 `repoPath` 时只查指定仓库。结果节点保存查询仓库——
  直接跳转与缺少位置时的补查都用它。
- 查询完成后展示符号详情（位置/签名/文档）、
  调用者、被调用者、上层调用链、下游调用链。
- **结果节点均可点击跳转定义位置**（新 CLI 的 callers/callees JSON 已带
  节点 file/line；缺位置的旧输出回退按 canonical ID 解析后再跳）。
- Activity Bar 一次查询完整深度（默认 10 层）后在内存中组装调用树，
  不再逐层启动 CLI；每棵树最多展示 100 个节点（防大图卡顿侧栏）。
- 旧版 CLI（rows 缺 source/target）自动回退逐层 `query callers/callees
  --depth 1`，避免扩展与二进制版本不匹配时丢失调用链。

### Output 面板

CLI 原始文本仍写入「codeintel」Output Channel，但查询、影响分析、更新
索引及错误**不会自动弹出面板**；查询完成后上层/下游调用链也会以文本树
同步写入面板（复用侧栏结果，不额外调用 CLI）。

### 生成调用时序图

对最近一次查询的符号执行 `codeintel query sequence --code --format
plantuml`，产物写到**最近一次成功查询的仓库**的 `<repo>/.codeintel/sequence/`
（文件名 = 符号短名 + canonical ID 哈希，避免同名覆盖）后自动打开：CLI 可渲染
时输出 PNG；PlantUML 渲染不可用时把回退的 Mermaid 文本改名 `.mmd` 再打开。

## 打包 / 安装（VSIX）

```bash
make plugin          # 生成 vscode-extension/codeintel.vsix（固定版本 vsce）
make install-plugin  # 打包后用 code --install-extension --force 安装
```

## 自检

```bash
node scripts/selfcheck-vscode-extension.cjs   # 定义定位/多仓库/跳转/时序图纯逻辑自检
```

## 配置（settings.json）

| 键 | 默认 | 说明 |
|---|---|---|
| `codeintel.binaryPath` | `codeintel` | CLI 路径（PATH 中或绝对路径） |
| `codeintel.repoPath` | 空 | 目标仓库根目录（空 = 当前工作区根；根无 go.mod 时遍历第一层含 go.mod 的目录） |
| `codeintel.autoUpdate` | `true` | 保存 .go 后防抖 2s 自动增量更新索引 |
| `codeintel.callChainDepth` | `10` | 调用链树/时序图展开深度（层） |
| `codeintel.maxTreeNodes` | `100` | 每棵调用链树最多展示的节点数 |

## 目录说明

- `extension.js`：扩展逻辑（输入 → execFile CLI --json → 树/文本渲染）
- `package.json`：manifest（命令/Activity Bar 容器/视图/标题按钮/配置）
- `media/codeintel.svg`：Activity Bar 图标
- 无构建步骤（纯 JS）；`node --check extension.js` 语法验证
