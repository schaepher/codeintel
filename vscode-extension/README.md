# codeintel — VS Code 扩展（最小可用，#233）

在编辑器内直接查询 Go 代码库智能索引：符号详情、调用者/被调用者、
改动影响预判、增量更新索引。**后端复用 codeintel CLI**（--json 契约，
docs/json-contract.md）——扩展只做输入与渲染。

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
| `codeintel: 查询符号` | 输入符号名 → 详情 + 调用者/被调用者列表 |
| `codeintel: 影响分析` | 输入符号名 → 改前影响节点列表 |
| `codeintel: 更新索引` | 增量重建（改了代码先更新再查询） |

输出面板「codeintel」查看结果。

## 配置（settings.json）

| 键 | 默认 | 说明 |
|---|---|---|
| `codeintel.binaryPath` | `codeintel` | CLI 路径（PATH 中或绝对路径） |
| `codeintel.repoPath` | 空 | 目标仓库根目录（空 = 当前工作区根） |

## 目录说明

- `extension.js`：扩展逻辑（输入 → execFile CLI --json → 渲染）
- `package.json`：manifest（命令/配置/激活事件）
- 无构建步骤（纯 JS）；`node --check extension.js` 语法验证

后续演进方向（#215 长期）：符号跳转（file:line 定位）、Hover 悬浮卡、
选中符号右键查询——均走现有 CLI/MCP 能力。
