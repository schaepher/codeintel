# 事后树：排障思路树（2026-08-23，Q242–Q244 会话沉淀）

定位方法论——怎么找到问题。分工：runbook.md 是故障模式速查表（给
「答案」：现象 → 处理 → 原因/预防）；本文给「找答案的路径」（症状 →
分支 → 根因）。防错设计（怎么让问题不发生）见「事前树」
prevention-tree.md。

## 主干：五步定位法

1. **复现并收症状**——错误信息第一行往往是答案（disk quota /
   no such column / syntax error 都直接命中过）
2. **隔离层次**——先分清「环境 / 测试 / 代码」哪一层：用最便宜的
   探针（echo / timeout 单测 / 直接查库）
3. **还原设计意图**——把「看似 bug」先还原成「有意设计」再动手
   （filterFKNoise、adj 去重、LIKE 兜底全是这类坑）
4. **用断言逼近真相**——手写诊断（临时 diag 测试）→ 测试驱动
   （为期望行为写测试，BFS 多路径真 bug 就是这么暴露的）
5. **修复后全量验证**——`go test -race -count=1 -p 1 ./...`
   （须 `TMPDIR=/home/schaepher/.tmp-build` 前缀），依赖升级也全量

## 分支 A：命令报错 / 编译失败

- 错误信息首行直读：
  - `disk quota exceeded` → /tmp tmpfs 配额满（runbook #1）→ 换
    TMPDIR；环境问题不修代码
  - `syntax error`（批量改写后的代码）→ 改写脚本有 bug → 断言式
    替换 + 立即编译
  - `no such column` → 先读 schema 再写查询——列名记错
    （source vs 真实 source_id）会让诊断结果本身失真
- 未知参数不报错 → 查解析器静默丢弃分支（--full 无效根因）

## 分支 B：测试挂起

- `timeout 30 go test -run ^TestX` 逐个定位
- 找「等一个不会来的响应」：MCP notifications/* 无 id 不响应 →
  只写不读；死锁 = 列出谁在等、被等方为何不回

## 分支 C：工具层持续失败（Bash/Edit 连续失败）

- 权限通道故障（cc-connect Stream closed）→ echo/pwd/cat 简单命令
  探测恢复；确认 hook 拦不拦（PreToolUse 只拦 Edit|Write 不影响 Bash）
- 勿连续重试浪费循环

## 分支 D：查询结果不对

- D1 返回 0 结果：先确认数据在不在（diag 测试直查 sqlite）→ 再理解
  过滤逻辑——filterFKNoise 有意丢 id→id 互查（fixture 须用非 id
  列名）；relation_candidates 有 marker 行（from_col=''）诊断勿当数据
- D2 路径缺失/候选数不对：先查表级去重（adj 同表对只留类型最优边 →
  多候选须不同中间表）→ 再看真 bug（BFS `seen` 阻止同层前驱 +
  `reached` 提前退出——测试驱动才暴露，功能从未工作过）
- D3 flag 不生效：查解析器（分支 A）
- D4 结果「太对」意外命中：下层有 fallback（GetSymbolByName LIKE
  兜底）→ 断言要验证走的哪条路径
- D5 数字不对：先核对断言本身（"1/3" 笔误应为 "1/2"）→ 再查计数
  范围（staleInfo 把 .codeintel/ 自身产物计入 → 排除）

## 分支 E：依赖升级

- x/tools v0.26→v0.42（go-sdk 连带）→ 升级后全量验证，不能只跑
  相关测试

## 分支 F：索引类

- codegraph 索引滞后 ~500ms——编辑后立刻查询不可靠，等一拍再查

每条分支的预防设计见「事前树」prevention-tree.md 对应层次。
