# 自举分析循环（Self-Analysis Loop）

> 以本项目（ana / codeintel）自身为分析对象，重复执行
> 「事实确认 → 生成 wiki → 新人视角覆盖度检查 → 必要时代理补充」
> 的改进循环。本文件是循环的**唯一记录**：背景、意图、方法、
> 每轮的分析/改进方案/实施结果。

## 背景

2026-08-23 用户设定长期工作模式：codeintel 的核心能力是"从代码
自动生成业务 wiki"，本项目自身就是最好的验证对象（自举）。用户
要求以此为中心重复做四件事，并以文档固定每轮产出，形成可积累的
迭代闭环。

## 意图（用户原话要点）

1. **事实确认**：通过事实获取当前项目的实际效果（索引/工具真实
   输出），不凭印象。
2. **解析形成 wiki**：尽量不依赖 AI——只在实在没办法的情况下才
   修改 yaml 辅助进一步的分析。
3. **新人视角覆盖度**：检查生成的 wiki 是否足够让一个新人快速
   了解项目的方方面面——关键模块和流程无死角、其他流程完整、
   **所有命令和 HTTP 接口都有单独的页面解析**。
4. **AI 杠杆总结**：静态分析做不到 3 时，才在提供足够信息的
   情况下引入 agent 做信息串联和补充；同时总结 AI 在哪些阶段
   做非常少的帮助就能获得非常好的效果。

## 方法（每轮固定四步）

| 步骤 | 内容 | 产出 |
|---|---|---|
| R#-1 事实确认 | 索引同步（update）、工具盘点（命令/接口/包清单）、wiki 生成 | 基线数据 |
| R#-2 覆盖度差距 | 新人视角逐项检查（见下方覆盖度清单） | 差距清单 |
| R#-3 改进实施 | 纯工具优先 → yaml 辅助 → AI 串联（依序降级） | commit + 验证 |
| R#-4 复检 + 杠杆总结 | 全清单复检 + AI 各阶段投入/效果对照 | 本节更新 |

**实施优先级**（用户方法论）：静态分析能力 > yaml 配置辅助 >
agent 信息串联。每轮先问"代码里是否已有事实可用"（help 文本、
注释、doc_comment、索引数据），再考虑改 yaml，最后才让 AI 写
内容——AI 写的内容标注来源（自动推断）与事实区分。

**交付约定**（用户 2026-08-23 指定）：**每轮结束生成单文件
wiki html 发给用户验收**（docs/wiki/index.html，cc-connect
send 附件）。

## 覆盖度基线（新人视角完整清单）

- [x] 概览页（架构图 + 模块目录 + 术语表）
- [x] 模块页（职责/入口/核心符号/相关表/包间调用图/流程时序）
- [x] ER 图（列级标注 + 模块过滤 + 交互版入口）
- [x] 表清单（字段/索引/DDL）
- [x] 命令页（全部顶层命令 + query 子命令，22 条）
- [x] 系统流程页（命令入口调用链，12 条流程）
- [x] HTTP 接口页（/api/* + /incremental + /wiki/*，16 个）
- [x] 包结构（包节点 doc_comment 职责地图，15 包）
- [x] 跨页搜索（模块/表/术语/工具）
- [x] 描述补全引导（缺口统计 + 横幅 + wiki --init 骨架）
- [x] 新鲜度（索引 commit 标注 + serve 自动刷新）
- [ ] 术语表（yaml glossary 未配——语义层，yaml/AI 候选）
- [ ] 表列说明（50 列无 comment——yaml/AI 候选）

## 轮次记录

### R1（2026-08-23）——工具面补齐

**分析**：索引同步至 HEAD；生成 wiki 基线——仅有模块页/表/ER，
新人视角缺：命令无页、HTTP 接口无页、包职责无地图。

**改进方案**（纯工具，零 AI 内容）：
- commands.md + /wiki/commands：usageText 常量抽出 + 解析成条目
  （数据源：CLI 帮助文本）
- api.md + /wiki/api：server 源码解析（mux.HandleFunc 路由 +
  handler 上方注释；数据源：代码注释）
- 包结构区块：新增 sqlite.Repo.GetPackages（KindPackage 节点
  doc_comment 即职责，噪音目录过滤）+ action 封装
- 导航/搜索索引/测试同步

**实施结果**：commit `d4bae09`（12 文件 +512 行）；实测 22 条
命令、16 个接口、15 个包；serve 冒烟 200；全仓 -race 全绿。
顺手修 bug：wiki 命令默认不读仓库根 wiki.yaml（与 serve 对齐）。

**AI 杠杆点**（R1 实证）：
- 差距识别（命令/接口清单 vs 页面盘点）：极少帮助、极高效果
- 数据源选择（知道现成代码事实在哪）：极少帮助、高效果
- 工具能力开发（GetPackages/解析器）：一次性投入、后续复用
- yaml 辅助：零（不需要）

### R2（2026-08-23）——进程视角 + 架构图

**分析**：R1 补齐后复检——命令/接口/包已覆盖；剩两差距：
系统级流程无页（新人不知道命令跑起来发生什么）、概览架构图空
（yaml architecture 未配时第一眼没系统图）。

**改进方案**（纯工具）：
- processes.md + /wiki/processes：12 条流程（root.go Main
  switch 事实映射命令→入口函数），每条 = 入口符号 + 调用链
  sequenceDiagram（ResolveSymbol + Callees 深度 2）+ 涉及包
- 架构图 fallback：yaml architecture 空时自动包间调用聚合图
  （全模块 PkgCalls 合并、同向计数相加、确定性排序），
  md/html/serve 三处统一

**实施结果**：commit `684afbc`（流程页）+ `379ee32`（架构图
fallback）；实测 init 流程 18 参与者完整链路；无配置场景自动
出图；全仓 -race 全绿。

**AI 杠杆点**（R2 实证）：
- 流程页：AI 只做 12 行"命令→入口函数"映射（switch 事实转写），
  调用链图全部自动——杠杆极高
- 架构图 fallback：纯工具（PkgCalls 已在 WikiData，缺聚合展示）
  ——AI 零内容投入
- 覆盖度全清单达成"工具面无死角"

### R3（2026-08-23）——语义层 AI 初稿（agent 串联补充场景）

**分析**：静态工具面复检 11 项全达成；剩余差距全在语义层——
术语表空（glossary 未配）、50+ 表列无说明（表清单大量空白列）。
静态分析给不出业务语义（列说明是业务知识），按方法论进入
「提供足够信息 → agent 串联补充」路径。

**改进方案**：
- 工具导出足够信息：`query table <表> --json` 导出 62 列数据
  （列名 + 读写上下文函数名——AI 推断的依据）
- AI 初稿：基于列名/表语义/读写上下文推断 62 列说明 + 9 表
  别名补充（2 处低置信标「待确认」）→ 建议 yaml（不直接改主
  文件）
- 合并：按「已有列保留（人工优先）、缺的补初稿」合并进
  wiki.yaml（AI 初稿模式，可 git diff 回滚/修改）
- 重新生成 wiki 验证渲染

**实施结果**：wiki.yaml 63 个 comment（62 补 + 1 原有）；表清单
空白列清零（除 2 处「待确认」）；docs/wiki 重新生成（md+html）。
**待用户 review**：wiki.yaml diff 确认/修改初稿。

**AI 杠杆点**（R3 实证——用户方法论第 4 点首次启用）：
- 信息导出：零 AI（工具现成 `query table --json`）——杠杆极高
- 推断 62 列说明：AI 约 10 分钟（读上下文 + 写初稿）vs 人工
  逐列写 2-3 小时——**杠杆最高的场景**；且标注「待确认」控制
  风险，低置信仅 2 处
- 合并/验证：脚本化（保留人工列优先）
- 结论：语义层是「AI 少帮助高效果」的主战场——前提是工具先
  给出结构化上下文（列名/读写函数），AI 只做推断不查代码

### R4（2026-08-24）——AST 主路径激活 + 别名列归属根治（重大）

**触发**：用户检查「内部调用链：extractRepoDir」图（内容真实但
顺序反直觉——steps 按 Caller 字典序导致链尾在前；修复为入口
优先 + 源深度排序）。随后追 edges「待确认」字段根因，挖出
**重大 bug**：

**发现**：vitess v0.24.0 的 `TableName` 是**值类型**实现接口，
代码断言 `.(*sqlparser.TableName)`（指针）**从未成功过**——
Q252 换库以来 AST 主路径全是死代码，所有 SQL 静默降级启发式
（Q252 声称的"18 形态覆盖 15"实为启发式结果）。AST 修复
（4 处值断言）后真正激活，连带修复：
- 别名列归属：SELECT 限定符列（n.name）映射回真实表，不再
  全归主表（edges.name/file_path 噪音从源头消失——索引节点
  数 0，yaml 屏蔽清理）
- $N 占位符：vitess 把 PostgreSQL `$1` 解析为 ColName
  （Name="$1"）而非 Argument——astWhereCols 补识别
- 隐式 dual 伪表：无 FROM 的 SELECT vitess 自动加 dual——
  跳过，子查询内表提取降级启发式保留
- 多层 JOIN：astJoinPairs 此前只取最内层 ON（外层被跳过且
  顺序反）——ON 提取独立于左右类型 + 递归后提取（书写顺序）

**验证**：ana reindex 节点 32385 → 33455（AST 提取更完整）；
edges.name/file_path 节点归零；全仓 -race 全绿；ssa 形态矩阵
+3 测试（别名归属/多 JOIN 顺序/$N）。

**杠杆**：用户一句话检查（图顺序）→ 挖出换库以来一直失效的
AST 主路径——**事实驱动的验证（query table 输出 vs schema
对照）比单元测试更早暴露**；AST 激活是"工具自身能力"的一次
质变（此前全部 SQL 解析都在降级路径上）。

### R3 补（2026-08-24）——edges「待确认」字段溯源 + 列级噪音隐藏

用户指出 edges 表两个「待确认」字段（name/file_path）应查设计。
**答案**：设计文档 + schema（db.go:47）定义 edges 仅 7 列
（id/source_id/target_id/kind/tool_source/confidence/metadata）
——name/file_path **不存在**，是 SQL 摘要的**别名列归属 bug**
产物：GetGrpcCalls 的 `n.name`、GetFrameworkStructs 的
`caller_n.file_path`（nodes 别名列）在列提取时去限定符后全部
归到 FROM 第一表 edges。

处理：
- wiki.yaml：两列标「解析噪音」+ hidden（新增 yaml columns
  列级 hidden 支持——mergeTableColumns hidden 集同时过滤自动列）
- 验证：edges 表清单恢复 7 真实列；TestMergeTableColumnsHidden
- **遗留**：解析器别名列归属 bug 本身未修（R4 候选——astSelect
  列提取需保留限定符映射回真实表）

### R6（2026-08-24）——降级可观测（fallback 感知增强）

用户诉求：凡是 fallback 添加信息增强感知——提前识别"一直降级"
（R4 教训：AST 主路径死代码静默半年无人察觉，直到用户检查图）。

盘点降级点（SQL 解析 AST→启发式 / scip 缺失 / relations 未算 /
update 全量降级），核心是 SQL 解析（最易静默）。实施：
- ssa 包计数器（atomic）：sql_ast_ok / sql_ast_fail / sql_heuristic
  ——parseSQLStmt 埋点（AST 成功 / 失败 / 启发式兜底）
- orchestrator：构建前 Reset + finishBuild 采集 → build_metadata
  新列 degrade_stats（JSON，ALTER 幂等补列）
- 展示三通道：init/update/reindex 构建报告"SQL 解析:"行 +
  wiki 概览（index.md + serve overview"构建 SQL 解析降级统计"）
  + repo_summary（Latest 带字段）
- 测试：TestSQLStats（计数/清零）

**实测**：ana reindex 降级统计 {"sql_ast_ok":72,"sql_ast_fail":43,
"sql_heuristic":43}——37% SQL 走启发式（动态 SQL 正常降级，但
此前完全不可见）；R4 场景（AST 全死）下现在会显示 ast_ok=0——
"一直降级"第一次构建即暴露。

### R7（2026-08-24）——架构图 AI 整理版 + SQL 降级调查

**① 包间调用图 AI 整理版**（用户诉求）：自动聚合图保留（数据
完整），新增 archMermaidCurated——过滤基础工具包（logging 无
业务信息）+ 临时包（seed），按入口/核心/支撑三层 subgraph
分组（规则固化确定性，不依赖运行时 LLM）。实测 26 边 → 13
条业务边。三处渲染新增「架构图（AI 整理）」区块。

**② SQL 降级调查**（43 → 31，结论：无缺陷可修）：
- VACUUM/PRAGMA 等 SQLite 命令 vitess 必失败且无信息——
  提前短路（不计入降级）
- 动态 SQL 多候选重复计数失真 → 去重（同 SQL 只计一次）
- 降级形态分类入库（dynamic/dialect/other）：31 条 = 15 动态
  拼接（预期）+ 8 SQLite 方言（INSERT OR，vitess 不支持，
  预期）+ 递归 CTE（vitess 偏 MySQL 对递归 CTE 支持不全——
  启发式已能提取表，兜底正确；量小不换库，分支 G）
- heur_other 持续可观测（未来新增非预期形态时关注）

### R8（2026-08-24）——枚举类型化收尾（无类型常量 → 带类型常量）

**触发**：用户用枚举检测器检查未被识别为枚举的常量——"是不是应该
定义为常量类型？"（R5 检测器发现 20 个无类型常量 8 组，本轮处理
其中 4 组真枚举）。

**分析**：4 组无类型常量（BuildStatus/ToolSource/RelationType/
SummaryAccessKind）全是 DB 列值（build_metadata.status / edges.
tool_source / table_relations.type / function_field_summary.
access_kind），跨包引用多、值易重复定义——类型化的价值最高。

**改进方案**：
- domain 定义 4 个 string 类型 + 常量带类型（entity.go 2 组 +
  ports.go 2 组）；字段类型化（Fact.ToolSource/BuildMeta.Status/
  TableRelation.Type/FunctionFieldSummary.AccessKind/
  BuildResult.Status）
- **编译器驱动转换**：改类型后 go build 列出全部类型不匹配点，
  逐一 string() 转换（map 键/slice/字符串拼接/比较）——不靠
  grep 猜，编译器 100% 覆盖引用点
- 测试辅助函数 findSummary 签名直接接受 SummaryAccessKind——
  比 N 个调用点逐个转换更稳（一次改动）
- 顺手修：precompute 计数、测试字符串拼接等字面量残留改常量
  引用（grep 复查编译通过但风格不一致的漏网之鱼）
- 顺手修：wiki html 单文件渲染遗漏构建降级统计（R6 三通道
  md/serve/html 对齐）

**实施结果**：commit `d8f4f7e`（28 文件）+ `36e6e5a`（1 文件）；
build + vet + 全量 -race 全绿；检测器实测默认识别 4 组
（--include-untyped 多出 8 组无类型）；索引 update 符号 33974；
wiki html 重新生成发送验收。

**AI 杠杆点**（R8 实证）：
- 枚举检测器（R5 工具）→ 找出候选 → 用户判定类型化——工具
  产出 + 人工决策，AI 零猜测
- 编译器驱动替换：误改风险最低的机械替换方式（gopls 对类型
  转换无一键重构，编译器错误清单即最可靠完备性证明；gopls
  references 可复核引用点）
- 遗漏检查：grep 字面量（编译通过但未用常量）找"漏网之鱼"——
  纯工具可发现非编译错误类的不一致

### R9（2026-08-24）——实体协作图（对象交互抽象 + 自举设计诊断）

**触发**：用户指出时序图太大（入口 cmdWiki 52 参与者/60 边），
提出"调用关系能否抽象成对象实体间的交互 + 实体内部交互？这样能
反过来优化项目本身的设计"——两轮 grilling 确认（实体定义/边语义/
双输出/自举与通用并存）。

**分析**：函数级调用链粒度太细（cmdWiki 直接调用面 17 函数 × 每
个再展开 2-5 个 → 深度 2 全展开 62 边）。索引数据齐备（203 struct
+ 9 interface + 320 has_method 边 + 36k calls 边），实体化可行。

**改进方案**（纯工具，零 AI）：
- 实体 = 有行为类型（struct/interface）+ 游离函数按包聚合门面
  （≥5 个才建；行为门槛过滤 DTO/缓存：1 方法 0 出边；接口豁免）
- 实体间边 = 方法互调聚合计数；实体内交互 = 节点内互调标注
- 4 类设计诊断（固定阈值起步）：跨包高耦合 ≥20 / 跨包循环 /
  上帝对象（≥40 方法或 ≥20 出边）/ 游离函数占比（≥8 且 > 包方法）
- 交付：query entities（文本/--json/--format mermaid）+ MCP
  entities 工具 + wiki 概览「实体协作」区块 + 流程页/模块页巨型
  时序图替换为实体协作子图（函数级细节保留 query callees）

**实施结果**：commit（R9 实体化，1 个）；cmdWiki 流程 52 参与者
→ 10 实体 + 10 聚合边；全仓 -race 全绿；wiki html 重新生成。

**自举首份诊断**（对 ana 自身）：
- 高耦合对：cli.cli→action.Actions 73 次互调（跨包唯一命中）
- 游离函数占比：cli 190 vs 26 方法、ssa 119 vs 77、ast 36 vs 21
  ——三个核心包大量函数未封装（真实设计信号）
- 上帝对象：sqlite.Repo 77 方法、action.Actions 60 方法、
  ssa.fieldExtractor 59 方法、server.Server 14 方法 52 出边
- 阈值调优过程本身即自举：首跑混入第三方/临时包 → module 前缀
  过滤；DTO 噪音 → 行为门槛；同包互调误报 → 跨包限定；阈值
  15→20 去除小类型误报

**AI 杠杆点**（R9 实证）：
- 用户提出抽象方向（实体间/内交互）→ 索引数据直接支持（has_method
  + calls 已有）——工具面零新采集
- 诊断阈值调优靠自举数据分布（首份报告驱动），AI 只做机制
- 实体化同时解决「时序图太大」与「设计可诊断」两个问题——
  一次抽象双收益

## 候选方向（未定优先级）

- yaml 语义层：术语表（glossary）、表列说明（50 列无 comment）、
  表别名（8 表）——AI 可从符号名/ER 推断初稿供用户确认，或用户
  手写
- 新人实测演练：挑一个陌生项目，用 wiki 走通 onboarding 流程，
  验证"新人视角无死角"是否真实成立
- 流程页深度：入口调用链 → 关键数据流（value-trace 串联）
