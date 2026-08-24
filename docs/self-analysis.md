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
- [x] 术语表（24 条配置齐全；R24 --ai 可从事实识别补缺）
- [x] 表列说明（R23 --ai 批量补缺 63 列 + 人工确认）

> 覆盖度清单 2026-08-24 全部勾选（R23/R24 后）——语义层不再依赖
> 手写，`wiki --ai` 一次请求补全全部缺口（模块描述/表别名/列说明/
> 术语表）。

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

### R10（2026-08-24）——实体协作区块解读层（信号 → 行动闭环）

**分析**：R9 交付后新人视角复检——实体协作区块只有一句概念介绍，
「门面190」「73 次互调」「高耦合对」无解释：新人不知门面是什么、
诊断意味着什么、该怎么做。「反过来优化设计」的闭环断在"信号"
这一环。

**改进方案**（纯静态文本，零 AI——图例与诊断解读是固定设计知识）：
- 图例：门面 N = 游离函数数、内 N = 实体内互调、边 N = 互调计数
- 每类诊断补「含义 + 建议」：高耦合→接口隔离/职责重分配、循环→
  抽公共依赖/反转依赖、上帝对象→按职责拆分、游离函数占比→类型
  封装
- 明确「诊断是信号不是结论——先 query callees 核实再重构」
- 四通道同步：md / html / serve / CLI 文本

**实施结果**：commit `aa49ee0`（2 文件）；md 25 处解读、html 含
全部诊断解读；全仓 -race 全绿；wiki 0.93s 生成（Entities 全量
查询无性能影响）。

**AI 杠杆点**（R10 实证）：
- 差距识别（新交付物缺解读层）：极少帮助、高效果——自举循环
  的复检环节持续发现"交付即缺口"
- 解读文本：固定知识（设计原则），写死进代码——零 AI 运行时
  依赖，与 R7 curated 规则同思路（确定性 > LLM）

### R11（2026-08-24）——术语表 + 表列说明清零（语义层收尾）

**分析**：覆盖度清单复检——术语表是唯一长期未勾选项（R2 起仅 6 条
基础缩写）；表清单提示 2 列无说明（R6/R9 新增列 R3 初稿没覆盖）。

**改进方案**：
- 术语表 AI 初稿（R3 模式复用）：18 条新增（索引/降级/启发式/AST
  主路径/字段追溯/值流/摘要/表关联/实体协作图/门面实体/上帝对象/
  增量构建/新鲜度/动态派发/置信度/降级统计/全局注册表/跨层摘要）
  ——wiki 高频领域词汇全覆盖，待用户 review
- 补 2 列说明：build_metadata.degrade_stats（R6 新列）、
  relation_progress.build_id——表清单空白列清零

**实施结果**：commit `d778d9c`（wiki.yaml +40 行）；术语表 24 条
渲染；补全提示消失（0 模块无描述/0 表无别名/0 列无说明——覆盖度
清单全部勾选完成）。

**AI 杠杆点**（R11 实证）：
- 术语定义来源是项目语义（AI 已知），yaml 初稿模式 10 分钟产出
  vs 人工整理 1-2 小时——R3 已验证的杠杆路径再次复用
- 列说明缺口是"新列未回填 yaml"的流程问题——R3 合并规则已覆盖，
  只需补数据；提示机制（wikiGapReport）让缺口可见

### R16（2026-08-24）——概览实体全图去噪（弱关联过滤）

**分析**：R14 布局后实体协作全图在第 2 位（收益最大章节），复检
发现 51 边中 1/3 是 count=1~2 的弱关联（如 Actions→CodeEntity
1 次）——无设计信息且撑大图；R15 拓扑排序后线方向已对，但噪音
边仍在。

**改进方案**（纯工具）：
- EntityMinEdgeCount=3：方法互调 < 3 次的边不画
- 仅弱边/无边的实体随之隐藏（图聚焦真实协作）
- 诊断不受影响（诊断用全量边——弱关联也是关联信号）

**实施结果**：commit `dde4ba9`（3 文件）；全图 51→30 边；
全仓 -race 全绿；wiki html 发送。

**AI 杠杆点**（R16 实证）：
- 复检环节（R10 同款）：连续两轮布局优化后，弱边噪音是下一步
  可感知差距——"交付即缺口"模式持续
- 阈值来自数据分布（16 条 count=1 是明显长尾）——自举数据
  驱动决策，非拍脑袋

### R17（2026-08-24）——模块页关键数据流（核心符号字段读写）

**分析**：候选方向 3（模块页深度）——模块页已有职责/入口/核心符号/
调用链，缺"模块处理什么数据"（读哪些字段、写哪些字段）。value-trace
能力已有（query summary/fields），缺模块级聚合展示。

**改进方案**（纯工具，复用 FunctionFields 能力）：
- wikiKeyFlows：核心符号（TopCallers）字段读写分组——direct_read
  归读、direct_write/indirect_write 归写
- 噪音过滤：本模块外字段（x/tools/ssa 等第三方）、map 访问
  （n["x"]/slots[key]）、[key] 变体归一——自举首跑暴露的噪音
- 用 canonical ID 而非名称（FromContext 跨包重名 ResolveSymbol
  多匹配失败——实测发现）
- 渲染 md/html/serve 三通道；serve snapshot 缓存（load 预计算）

**实施结果**：commit `fcd2367`（8 文件）；模块页核心符号区块后新增
「关键数据流」（Open 写 DB/DB.repoPath、fieldExtractor.emitValue
读写业务字段一目了然）；全仓 -race 全绿；wiki html 发送。

**AI 杠杆点**（R17 实证）：
- 能力复用（FunctionFields 现成）+ 聚合展示——零新数据采集
- 两个实现期 bug（跨包重名、第三方字段噪音）都是自举实测暴露
  而非预想——"事实驱动调试"持续有效

### R18（2026-08-24）——方法值传参盲区（fileCtx 孤立依赖根治）

**触发**：用户检查实体协作图发现 fileCtx 无入边——"出现一小块的
孤立依赖关系"。

**调查**：fileCtx 出边 34 条但入边 0；全量 calls/uses 边都没有
外部调用。根因：`ast.Inspect(f, ctx.visit)` 是**方法值传参**（非
调用表达式）——AST 适配器"函数作为参数"分支只建 passes_to
（接收者 Inspect→visit），无 调用者(Adapter)→方法(visit) 的
calls 边。Adapter 实际调用了 fileCtx 的方法（创建对象 + 传方法
值回调），索引无此关系——形态盲区（P2-1 函数值盲区同类）。

**修复**（测试先行 TestMethodValueArgCalls）：
- emitcall 回调分支：方法值实参（SelectorExpr）额外建
  callerID→paramID calls 边（带 line_num）
- 普通函数回调不建（passes_to 已表达——避免 unused 语义波动）

**验证**：reindex 后 (Adapter).processFile→(fileCtx).visit 出现，
实体图 Adapter→fileCtx count=1——孤立块消除；全仓 -race 全绿；
wiki html 发送。

**AI 杠杆点**（R18 实证）：
- 用户看图发现孤立依赖 → 追到索引形态盲区（第三个同族盲区：
  P2-1 函数值、本次方法值）——**实体图可视化成为索引缺陷的
  放大器**（R9 实体化让不可见的关系缺失可见）
- 修复模式复用（回调分支 + 条件收敛防误报）

### R19（2026-08-24）——表字段类型自动填充（sqlite_master 事实源）

**分析**：用户要求字段类型太多没填，不借助 AI 尽可能地填。类型
事实源 = SQLite schema（sqlite_master 的 CREATE TABLE 权威）；
现状渲染只从 yaml/gorm tag 取（gorm tag 仅 ORM 结构体列有）。

**改进方案**（纯工具）：
- parseCreateTableSchema：CREATE TABLE 解析列类型/默认值——约束
  行跳过、引号列/多空格/行尾注释/前导逗号（ALTER 加列形态）/
  无空格行越界全兼容（4 个实现期边界 bug 全由测试暴露）
- 填充优先级 yaml > schema > gorm tag（yaml 人工可覆盖）
- 渲染层自动（md/html/serve 三通道 + serve snapshot 缓存）

**实施结果**：commit `5a3ab12`（11 文件）；63 列中 53 列有类型
（84%）——剩余 10 列全是 repos 全局注册表（schema 在
~/.codeintel，仓库内无事实源，留 yaml 补）；全仓 -race 全绿。

**AI 杠杆点**（R19 实证）：
- "不借助 AI 尽可能地填"——SQLite 自身 schema 就是零 AI 的
  权威答案；解析器一次性投入、所有仓库复用
- 边界形态（ALTER 逗号/引号列/注释）测试先行暴露——纯工具
  路径同样需要形态矩阵验证（morphology-matrix 记忆适用）

### R20（2026-08-24）——表关联结构体展示（TableName 反查 + 可折叠核对）

**分析**：用户要求表定义上方展示关联结构体代码（可折叠展开核对
字段映射）。本仓库表是 SQL 直写（无 ORM 结构体）——功能面向有
ORM 映射的外部仓库。

**改进方案**（纯工具，R5 枚举检测器同模式——源码扫描不依赖索引）：
- scanORMStructs：go/parser 扫 `func (T) TableName() string {
  return "tbl" }` → 表↔结构体；fset 定位结构体定义提取源码片段
- 渲染：md `<details>` 折叠 + html/serve fold-btn（模块区块同机制）
- 表详情每表上方；fixture 测试覆盖（order_tab↔Order 源码片段）

**实施结果**：commit `f09e8d7`（8 文件）；全仓 -race 全绿。
实现期典型 bug：循环变量名语义反转（tableOf 是类型名→表名，
`for tbl, typeName := range tableOf` 配对永不匹配）——调试 10 轮
暴露"名字误导"类 bug（变量名应表达语义）。

**AI 杠杆点**（R20 实证）：
- 结构体↔表关联的事实源在源码（TableName 方法）——零 AI；
  复用 R5 源码扫描模式
- 本仓库无数据 → fixture 测试是唯一验证途径（形态矩阵验证适用）

### R21（2026-08-24）——结构体 Go 类型作为表字段类型最终 fallback

**分析**：R19 填了 84%（53/63 列），剩 10 列是全局注册表（无 schema
事实源）。用户要求：结构体字段的 Go 类型作为最终 fallback——ORM
结构体字段是另一层零 AI 事实源。

**改进方案**（纯工具）：
- ormColTypes：scanORMStructs 结果（R20）的字段 Go 类型 → 表列映射
  （gorm column tag 优先、无 tag snake_case）——yaml/schema/gorm tag
  都无类型时的兜底
- snakeCase 修正：连续大写不拆（ID→id 而非 i_d）——prevLower 标志，
  只在小写后遇大写才拆

**实施结果**：commit `fd2c1e3`（3 文件）；fixture 验证 order_id→int64、
order_no→string、created_at→time.Time；全仓 -race 全绿。

**AI 杠杆点**（R21 实证）：类型链 yaml > schema > gorm tag > Go 类型
四层事实源逐级兜底——每层都有不可替代的来源（人工 > 数据库 > 声明
> 映射约定），AI 只补最后剩下的语义说明。

### R22（2026-08-24）——字段顺序还原结构体序 + 自增列第一

**分析**：ER 图和字段表顺序应与结构体定义一致（用户核对依据），
自增列排第一（数据库惯例）。

**改进方案**（纯工具）：
- 自增识别：gorm autoIncrement tag + schema INTEGER PRIMARY KEY
  （rowid 别名）
- 排序：自增第一 + 结构体字段序 + 其余稳定追加
- ORM 结构体字段成为字段行兜底来源（无 schema/yaml 时也有行）

**实施结果**：commit `8e495fa`（6 文件）；fixture 验证
[order_id(自增) order_no created_at]；全仓 -race 全绿。

**AI 杠杆点**（R22 实证）：顺序/自增都是代码里已有的事实（声明顺序
+ tag）——零 AI；「自增第一」是数据库常识，yaml 可覆盖。

### R23（2026-08-24）——AI Agent 选择 codex/claude（#0 遗留功能）

**分析**：用户提出"AI Agent 支持选择 codex 和 calude"。grilling
Q1-Q8 定案：用途=wiki 语义补全自动化 + 通用 ask 接口；后端=本地
CLI 子进程（codex exec / claude -p，零密钥）；选择=--agent 参数 >
~/.codeintel/config.yaml 默认 > auto 检测；产出=直接写 wiki.yaml
（git diff 可回滚）；wiki --ai 增量只补缺；ask 轻量自动打包
（问题中符号/表名精确识别）；三层降级（CLI 缺失报错/超时跳过/
解析失败重试一次）。

**改进方案**（新功能，测试先行 18 个单测）：
- agent.go：runAgentExec（claude -p / codex exec，ctx 超时中止）+
  resolveAgentWith 纯函数四通道选择 + ~/.codeintel/config.yaml 读取
- ask.go：cmdAsk（--agent/--symbol/--table/--timeout/--json）+
  packAskContext（token 精确匹配表名/符号 → 附加 callers/callees/
  表列上下文）
- wiki_ai.go + wiki_ai_merge.go + wiki_ai_parse.go：缺口收集（复用
  wikiGapReport 逻辑）→ 逐条 prompt → AI 初稿 → yaml.Node 合并
  （保留注释 + # AI 初稿 标注）→ cfg 同步（渲染用更新后配置）
- 根命令注册 ask；wiki 增加 --ai/--agent

**实施结果**：commit `65687a1`（11 文件）；全仓 -race 全绿；
真实冒烟：ask "main 函数是做什么的？" 自动识别符号、回答引用
file:line 准确；wiki --ai 本仓库 0 缺口（wiki.yaml 已完整）。
实现期修正：fake CLI 子进程持有 stdout 管道致超时测不过（exec
sleep 替换）；PATH 未隔离致测试真调 claude（隔离修正）。

**AI 杠杆点**（R23 实证）：
- 缺口统计（无描述模块/无别名表/无说明列）已有 → 增量触发天然
  省 token；AI 只填缺的，人工内容永不被覆盖
- yaml.Node 编辑保留注释 → git diff 可回滚 → AI 产出可审查可反悔
  ——「AI 写文件」的安全底线是版本控制，不是拒绝写入

### R24（2026-08-24）——R23 修复补遗 + 批量补缺 + 术语表接入

**分析**：R23 功能上线后三次真实验证暴露两个问题：
1. `--yaml` 与 `--out` 同目录时 AI 写回的 wiki.yaml 被渲染器
   `os.RemoveAll(outDir)` 连带删除（save 成功、文件却没了）——静默丢配置
2. 逐条调用（23 次）在真实 claude JSON 模式 60s 超时下 9 条失败；
   用户要求同会话 resume + 缺口合并一次请求

**改进方案**：
- cleanWikiOutDir：渲染器不再 RemoveAll 目录，只删已知产物文件
  （全局页 + 本次模块页）——wiki.yaml/笔记保守保留（bae2062）
- save 空文档防御：空 DocumentNode 无法编码（yaml.v3 document end），
  save 前初始化根 mapping（bae2062）
- claude 升级官方 JSON 模式：-p --output-format json 提取 result；
  旧版 CLI 回退原文（bae2062）
- 批量一次请求：缺口（模块+表+列）合并单 prompt，AI 一次返回完整
  YAML（wikiBatchOut）→ 整体合并；23 条缺口 1 次调用 0 失败（cf5b747）
- 同会话 resume：JSON 输出提取 session_id → 后续调用自动
  --resume <会话>；aiTimeout 60s → 120s（cf5b747）
- 术语表接入批量：prompt 四区「术语表」——AI 从给定事实识别
  3-8 个术语定义；列说明 prompt 带表间关联事实（rels 读写上下文）

**实施结果**：commit `bae2062` + `cf5b747` + 本轮（未提交）；
真实验证：删除 wiki.yaml 后批量 --ai 补全 23/23（0 失败）、AI 版
wiki.yaml 落盘保留、术语/列说明与人工配置高度重合；覆盖度清单
全部勾选；全仓 -race 全绿。

**AI 杠杆点**（R24 实证）：
- 「报告说写回但文件没了」→ 怀疑渲染阶段而非 save（RemoveAll 清
  目录是高危操作，--yaml 与 --out 同路径必踩）——工具 bug 归因
  要看完整生命周期
- 批量化是 AI 调用的第一优化：23 次 → 1 次，超时失败 9 → 0；
  一次请求全部带上还顺带解决上下文连贯
- 术语表不需要单独来源——AI 从既有事实（模块/表/列）识别即可，
  批量 prompt 加一个区块零成本

### R25（2026-08-24）——批量补缺分批落地（aiBatchMax 生效）

**分析**：R24 事实确认发现 aiBatchMax（60 条上限分两批）是悬空常量——
用户 R23 明确要求「实在有必要才分两次处理」，一次全量对缺口 >60
的大仓库有超 token 风险，分批未落地是真实差距。

**改进方案**：
- wikiAIFill 分批循环：缺口 ≤ aiBatchMax 一次全量；否则第一批
  模块+表别名、第二批列说明+术语表——两批同会话 resume（AI 保留
  第一批上下文，第二批 prompt 不再带已处理模块）
- aiBatchGaps 分块结构；失败计数按批统计

**实施结果**：commit 待提交；测试 TestWikiAIFillSplitBatches（61
模块+1 表+1 列组 = 63 缺口 → 2 次调用、第二批不含已处理模块、
计数 63/0）全绿；全仓 -race 全绿，verify.sh 通过。

**AI 杠杆点**（R25 实证）：
- 「悬空常量」是自举循环事实确认的典型猎物：声明了阈值却没消费
  处——要么实现要么删，半成品是债务
- 分批的边界由缺口数决定（≤60 一批），与 AI 能力无关——工具
  决定调用形态，AI 只响应

### R26（2026-08-24）——ask 交互模式（REPL，多轮追问同会话）

**分析**：候选方向「ask 支持交互式 REPL（多轮追问复用上下文）」——
R23 的 resume 机制（session_id 自动 --resume）已就绪，只差交互循环。

**改进方案**：
- `codeintel ask`（无问题参数）进入 REPL：`ask> ` 提示逐行读 stdin，
  exit/quit/q 退出、Ctrl-D（EOF）结束
- 每轮 buildAskPrompt（首轮自动打包符号/表上下文；追问轮不再重复
  打包——resume 已带前文，避免重复 token）
- buildAskPrompt 抽出（单次/REPL 共用）；单次模式行为不变

**实施结果**：commit 待提交；测试 TestCmdAskREPL（stdin 模拟两轮：
首轮带符号上下文、追问轮无重复上下文、两轮回答都输出、正常退出）
全绿；全仓 -race 全绿，verify.sh 通过。

**AI 杠杆点**（R26 实证）：
- REPL 的价值不在交互本身，而在「追问不重复付上下文费」——resume
  复用让多轮成本 ≈ 首轮 + 增量
- 单次/交互共用 prompt 构建（buildAskPrompt）——同一语义两处入口，
  上下文打包逻辑只写一次

### R27（2026-08-24）——wiki 对话界面 + Q&A 收集 + --with-qa 参考资料（W1/W2/W3）

**分析**：用户提出三连需求——wiki 页面提供对话界面（连 agent 深入
问问题）；Q&A 收集到 db；创建 wiki 时参数控制是否读历史问答作参考
资料（提升 wiki 品质）。grilling 定案：--with-qa、符号/表名相关性
匹配、固定侧边面板。

**改进方案**：
- W2 qa_history 表（schema 加法迁移 + schemaCols）+ SaveQA/
  QAForSymbols（context/question LIKE 匹配，时间倒序 limit）；
  ask 单次/REPL 回答成功后收集（saveQA 静默失败）
- W3 wiki --ai --with-qa：缺口表名/模块短名 → QAForSymbols 前 5 条
  → 批量 prompt 五区「历史问答参考」（可参考不照抄）
- W1 serve wiki 对话界面：POST /wiki/ask（question → buildAskPrompt
  → agentRunner → saveQA → JSON 回答）+ 固定右侧可折叠 chat 面板
  （chatPanelHTML 注入 serve 版页面，单文件 html 不注入）；
  claudeSessionID 加互斥锁（serve 并发安全）

**实施结果**：commit 待提交；测试：TestQAHistory（存储/匹配/limit）、
TestCmdAskCollectsQA、TestWikiAIFillWithQA（相关进 prompt 无关不进）、
TestWikiServeAsk（端点回答 + 收集）全绿；全仓 -race 全绿，verify 通过。

**AI 杠杆点**（R27 实证）：
- 对话即数据：用户与 AI 的问答沉淀成 qa_history，下一次 wiki 生成
  自动复用（相关性匹配进 prompt）——AI 辅助的飞轮效应
- serve 多请求并发暴露共享状态风险（claudeSessionID）——HTTP 化
  任何包级可变状态都要先问并发安全

### R28（2026-08-24）——go2o AI 剩余缺口清零 + 批次/超时再调优 + F1 遗留 processes 页入口化 + ER 图表名清洗

**分析**：交接遗留 0——go2o 增量 `wiki --ai`（复用 go2o-ai/wiki.yaml）
补 31 表别名 + 283 列说明。首跑 38 条成功/30 条超时（120s），重跑
同批再超时——**稳定慢不是偶发**（30 组 ≈200 列名 claude 生成超
120s；此前 38 组 ≈150 列名成功——150 是安全线）。渲染后 wiki-check
5/7 暴露两个既有问题：① mermaid 1 块坏（动态表名 `pt_%s`——go2o
源码 fmt.Sprintf 拼接表名，`%` 是 mermaid 语法错误）；② 时序 FAIL
（12 无调用链）——**F1 只修了 commands/api 页，processes 页还硬编码
codeintel 自身 12 个命令**（目标索引里当然无调用链）。

**改进方案**：
- 批次/超时双保险：aiBatchMax 30→20、aiBatchMaxCols 300→200、
  aiTimeout 120s→240s（连续两次超时实证后调）
- processes 页改从目标仓库 main 入口生成（对齐 F1 方案：entrySymbols
  入口 + 一级调用逐条展开深度 2 调用链）——**一级调用用完整 canonical
  ID 解析**（短名 pkg:name 无法按名解析，go2o 实测 app:ParseFlags
  解析失败）
- renderERMermaid 表名清洗（erEntityName：非 [a-zA-Z0-9_] → _；
  列 label 引号内不受影响）

**实施结果**：44 条补全/0 失败/0 跳过（缺口清零：150 表全有别名与
列说明）；wiki-check 5/7 → **7/7 PASS**（mermaid 45 块全过、时序
1 无调用链 ≤ 1 模块）；全仓 `-race` 13 包全绿；测试新增
TestRenderProcessesFromEntry（processes 页不再含 codeintel 命令）、
TestWikiERMermaidSpecialName（pt_%s → pt__s），TestWikiAIFillSplitBatches
断言随 aiBatchMax 更新（3→4 批）。

**AI 杠杆点**（R28 实证）：
- 超时判据：同批重跑再超 = 稳定慢 → 降批次/加超时；偶发慢才可重试
- F1 验收模式复现：外部项目验收暴露的硬编码（commands → processes
  同源 bug）——自举验证不覆盖"对外项目不硬编码"这一维度，需逐个
  页面核查数据源
- 测试环境教训：TMPDIR 指向 git 仓库内目录会使 TestIndexNonGitDir
  假失败（t.TempDir() 在仓库内 git log 向上命中 .git）——临时目录
  须在仓库外（.tmp-build/gotmp）

### R29（2026-08-24）——grpc 路由分析（query grpc-routes）+ grpc 枚举（proto 并入 query enums）

**分析**：用户选高优先级待办 1 的 grpc 部分 + 待办 6。事实调查发现：
索引**已有**服务端数据（§18 markServiceEntry 建 grpc_service 节点 +
grpc_impl 边 + serves_grpc 标记 + ast_grpc.go 客户端 grpc_call 边），
缺的是**方法全集**——grpc_call 只含被客户端调用过的方法，服务端定义
全集在生成代码 ServiceDesc。grilling 定案（Q1-Q6）：query grpc-routes
新命令（JSON 契约：服务/实现/注册点/方法）、数据源用生成代码、
proto 枚举自动并入 enums（source 标注）、本轮不做 http resolver。

**改进方案**：
- `query grpc-routes`：grpc_service 节点 → RegisterXxxServer 函数
  （生成代码）→ calls 入边（注册调用点 line_num）→ grpc_impl 边
  （实现）→ **ServiceDesc go/parser 提取方法全集**（MethodName+Handler）
- impl 追实现：注册参数为接口（inject 形态）→ implements 边追实现
  struct；排除 protoc 生成桩（Unimplemented*——SCIP 对隐式实现不输出
  implements 边，首个命中总是桩）
- `query enums` 扩展：extractEnums **全仓扫**（原限 internal/，go2o
  pkg/ 下枚举缺失）+ 跳过 .pb.go 生成代码 + 并入 .proto 源枚举
  （Source: go|proto）；proto 轻量词法解析（不调 protoc：enum 块/嵌套
  message 前缀/值号/注释（上一行与尾注释）/package 缺失回退目录名）
- MCP 工具 grpc_routes（staleWrap + repoAbs 闭包，对齐 entities 模式）

**实施结果**：测试 TestExtractProtoEnums（顶层/嵌套/注释变体/option
容忍）、TestExtractEnumsMergesProto（go+proto 混合、.pb.go 排除）、
TestQueryGrpcRoutes（fixture 索引 + ServiceDesc）全绿；全仓 -race 13
包全绿。**go2o 实测**：grpc-routes 31 服务（方法全集 + handler +
注册调用点行号 pkg/grpc/grpc_server.go:30-63 与实际代码完全吻合）；
enums ~140 条（订单状态/支付方式/钱包流水等 proto 枚举全带中文注释
+ 2 条 Go 枚举——全仓扫修复）。

**AI 杠杆点**（R29 实证）：
- 复用索引既有节点（grpc_service/grpc_impl/serves_grpc）而非新分析——
  "通过已有节点发现"的待办语义，缺口只剩方法全集（ServiceDesc 生成
  代码补）
- SQLite 单连接嵌套查询死锁复发（AGENTS.md 已警告）——registerCallSite/
  grpcImpl 内层查询前必须显式 Close 外层 rows
- SCIP 对 Go 隐式接口实现不输出 implements 边 → Unimplemented 桩误
  命中；排除生成桩后降级为接口名（数据源限制，接口名+位置可接受）
- proto 词法解析 ~150 行替代 protoc（值号/注释/嵌套全支持）——
  分析型工具优先轻量实现，重依赖后置

**R29 补遗（签名识别，用户当场要求）**：注册函数识别从"函数名
RegisterXxxServer + .pb.go 后缀"改为**签名识别**（collectRegisterServers
重写）——① 第一个参数类型是 grpc.ServiceRegistrar 接口或 *grpc.Server；
② 函数体调用 RegisterService 方法（protoc 生成与手写通用，最强信号）；
服务名从参数 2 类型去 Server 后缀提取（impl struct 形态回退
RegisterService 第一实参 desc 名）。注册函数节点打 `registers_service`
属性（nodeFor 的 extra 只支持 bool——直构节点存字符串），查询层按
属性找 Register 函数（不再按名推导）。go2o 复验 30 服务全命中（与
旧识别一致）；新测试 TestGrpcRegisterBySignature（手写 setupAll 形态）
+ TestGrpcNonRegisterNotMarked（普通函数不带属性）。旧 fixture 两处
（TestGrpcClientCallEdge/TestIndexHTTPHandlerLeaves 的 `s any` 简化
参数）更新为 Registrar + RegisterService 调用形态。

**R29 补遗 2（接口方法模式识别，用户进一步要求）**：再加**服务接口
本身的方法签名识别**（markGrpcServiceInterfaces）——"通过类型是否
实现了 grpc 的方法来判断"：grpc 方法参数/返回值是固定模式——每个
方法末返回值是 error，且首参是 context.Context 或参数/返回值含
google.golang.org/grpc 类型（流式）；接口全部方法符合 → grpc 服务
接口（不依赖注册点/函数名/文件，命名任意）。发射 grpc_service 节点
含 `methods` 属性（手写服务无 ServiceDesc 时的方法源——查询层
ServiceDesc 优先、属性回退）。go2o 复验：仍 30 服务（接口模式与
注册签名双通道一致，Repository 类接口方法形态不符合模式——零误报）；
新测试 TestGrpcServiceInterfaceByMethod（手写 HandService 无注册点）
+ cli 回退路径（fixture 手写服务 methods 属性）。

### R31（2026-08-24）——HTTP 路由分析（query http-routes，待办 1 http 部分）

**分析**：用户要求"用同样的方法处理 http，两个 resolver 各自实现
自己的识别模式"（grilling Q1-Q3 定案：独立命令 `query http-routes`
JSON 契约 method/path/handler/handler_file/register/resolver；gin 只
识别路由方法不含 Static；原生识别 ServeMux 方法调用形态）。现有基础：
markServiceEntry 只标记 serves_http + handler 节点（包级
http.Handle/HandleFunc），无路由清单产出；routes.yaml 仍是人工表。

**改进方案**（每注册点发射 http_route 节点——复用已有 KindHTTPRoute）：
- **原生 resolver**：包级 http.Handle/HandleFunc（markServiceEntry
  分支扩展）+ ServeMux 方法调用（mux := http.NewServeMux();
  mux.HandleFunc——emitSelectorCall 新分支，isServeMux 类型判断）；
  method 空（HandleFunc 匹配所有方法）
- **gin resolver**：*gin.Engine/*gin.RouterGroup 的 GET/POST/.../Any
  方法调用（isGinRouter 类型判断）；Group 前缀拼接——变量赋值继承
  （scanGinGroups 两遍收集）+ 一级链式（r.Group("/v1").GET——emitcall
  的 sel.X 非 Ident 分支，ginChainedPrefix 递归解析）
- handler 名形态矩阵：函数 Ident / http.HandlerFunc(f) 包装 / 方法值
  x.Method（ana serve 的 s.handleRoots 实测遗漏后补）
- 查询层 cmdHTTPRoutes：读节点输出（resolver→method→path 确定性排序）

**实施结果**：测试 TestHTTPRouteNative（4 形态：包级/HandlerFunc/
ServeMux/method 值）、TestHTTPRouteGin（replace 本地模块构造真实
gin 包路径——形态矩阵要求；GET+POST 同路径、Group 继承、链式、
Static 排除）、TestQueryHTTPRoutes（契约 JSON + 排序）全绿；全仓
-race 13 包全绿。**自举验证**（交接遗留 5 顺带完成：ana reindex
39609 符号）：`query http-routes --repo ana` 输出 **26 条路由**
（internal/server 的 ServeMux 全部识别——方法值 handler 形态）；
go2o 0 条（无原生/gin 路由——其他框架，符合"后置"语义）。

**AI 杠杆点**（R31 实证）：
- 复用 grpc-routes 模式（构建期识别发射节点 + 查询层直读）——
  "同样的方法"让 http 实现成本大幅下降（~400 行含测试）
- 自举即验收：ana 自身的 ServeMux + 方法值 handler 形态暴露
  routeHandlerName 盲区（SelectExpr 漏处理）——真实项目形态矩阵
  是测试 fixture 覆盖不了的
- gin 链式（sel.X 是 CallExpr）与变量式（Ident）是两条路径——
  emitcall 的 xid 提取只认 Ident 是既有假设，链式需独立入口
  （emitGinChainedCall）

**R31 补遗（gin 注册形态盘点，用户追问"能否识别"）**：核对后补三个
缺口——① `r.Handle("GET", "/path", h)` 通用注册（method 在 args[0]）；
② 匿名函数 handler（FuncLit → "(匿名)" 标注，不再丢整条路由）；
③ 多 handler（`r.GET("/x", mw, h)` 中间件在前——取最后一个为业务
handler）。链式（emitGinChainedCall）同步同形态。测试扩展断言三条
新形态。静态资源（Static/StaticFS）仍排除（Q2 定案）；NoRoute/NoMethod
兜底路由与动态循环注册（for + 变量路径）是已知盲区。

### R32（2026-08-24）——图渲染双引擎（plantuml + mermaid，待办 2）

**分析**：待办 2——wiki 图支持 plantuml 与 mermaid，默认 plantuml（Q3
定案）。grilling 定案：范围 = wiki 的图（ER/实体/架构/流程时序；查询类
--format mermaid 不动）；渲染方式 = 本地 plantuml.jar PNG（用户指出
/opt/plantuml/plantuml.jar）。实测语法发现 plantuml 与 mermaid **同名
图类型语法不同**（erDiagram 关键字不支持、sequenceDiagram 头不需要、
-->|N| label 不合法需冒号）——必须转换器而非复用。

**改进方案**：
- mermaid → plantuml 转换器（三类）：ER 删头+实体名行（纯关系行自动
  建实体）；sequence 删头（participant/消息行两引擎相同）；graph 统一
  （node "label" as id 定义 + 边行保留；自环边过滤——plantuml 不支持；
  -->|N| → --> N : 冒号 label；纯节点行跳过裸 id；subgraph 需 {）
- 渲染器：java -jar plantuml.jar -tpng -pipe（stdin/stdout）；jar 路径
  PLANTUML_JAR > /opt/plantuml/plantuml.jar；**java tmpdir 必须仓库外**
  （-Djava.io.tmpdir——ImageIO 写 /tmp 缓存 EDQUOT 直接 I/O error）
- --diagram 贯通：renderCtx.Diagram + diagramMD/HTML helper（md 输出
  plantuml 文本块；HTML PNG base64 <img> 单文件自包含；失败降级文本块）；
  全部 ~16 个 mermaid 发射点替换；plantuml 模式不引 mermaid CDN；
  serve 固定 mermaid（浏览器渲染）
- 调试利器（用户提示）：plantuml.jar --check-syntax 只检查语法不渲染
  ——定位语法错误从"渲染 PNG 猜"变成"直接报错行"

**实施结果**：go2o 实测 **35/37 图渲染成功**（95%）——2 个降级文本块：
subgraph 分组图（plantuml 组件图 subgraph+node 组合全语法错误——生态
坑，注释标注）与 80KB 巨 ER 图（渲染超时/内存）；wiki-check 7/7 PASS
（plantuml 模式无 mermaid 块自动通过）；测试 TestMermaidGraphToPlantuml
（节点双格式/自环/冒号 label）、TestMermaidERToPlantuml、
TestMermaidSequenceToPlantuml、TestMermaidGraphSubgraph、
TestWikiPlantumlDefault（PNG base64 + 无 CDN + md plantuml 块）；全仓
-race 13 包全绿。

**AI 杠杆点**（R32 实证）：
- 语法兼容性不能凭"同名图类型"假设——erDiagram 关键字/-->|N| 等
  plantuml 都不认，--check-syntax 逐项实测二分定位（用户提示的调试
  捷径：语法检查秒级 vs 渲染 PNG 秒级+文件）
- 大图渲染降级为文本块是合理设计（80KB ER 图 PNG 不实用）——
  降级不丢信息（文本可自行渲染）
- java 子进程继承 /tmp 配额问题（-Djava.io.tmpdir）——外部进程的
  临时目录假设与本环境冲突时，环境变量覆盖是通用解法

### R33（2026-08-24）——ER 图按业务领域分组 + mermaid 500 边阈值降级

**分析**：用户两条——① ER 图按业务领域划分（领域间一张 + 各领域各自
一张，同 F2 实体图模式）；② mermaid 超 500 边无法渲染，给方案参考。
调研发现两问题同源：go2o 完整 ER 图 **1283 条关系**（80KB puml 渲染
超时/超 mermaid 限制）。方案②给 5 个参考，用户选 **B+A**（分组治本 +
阈值降级兜底）。

**改进方案**：
- 表 → 领域：**表名前缀**（_ 前首段——go2o 实测 item_/mch_/member_/
  order_/trade_ 前缀与领域目录对应；无 _ 归 other）；splitERDomains
  按领域分组直接关系（fk/query 口径同 renderERMermaid），领域内边进
  组、跨领域边分离（领域间图数据源）
- 渲染：领域间关系图（跨域边表级标注）+ 每领域内部图（折叠 details
  同 F2）；md/html/plantuml 三版
- 方案 A：diagramEdgeCount（--> 与 ||-- 计数）> 500 → mermaid 模式
  降级（尝试 plantuml PNG 或提示文本）；plantuml 模式同样检测（1283
  边 ER 图白等渲染 30s 失败——直接提示）
- 完整 ER 图保留（超限提示引导用领域分组图/query relations）

**实施结果**：go2o 实测 **PNG 图 35 → 55**（新增 20 张领域图 + 领域间
图——全部渲染成功，原 1283 边巨图按领域切分后每图几十条）；降级文本
块 3 → 1（仅 subgraph 生态坑）；wiki-check 7/7 PASS；测试 TestSplit
TableDomain/TestSplitERDomains/TestDiagramEdgeCount/TestDiagramHTML
OverLimit 全绿；全仓 -race 13 包全绿。

**AI 杠杆点**（R33 实证）：
- 两问题同源识别：渲染失败（80KB）与 500 边限制（mermaid）其实是
  同一个"图太大"问题——分组方案一石二鸟
- 表名前缀是项目自身的领域元数据（无需 AI/配置）——命名约定即
  领域划分依据（其他项目可加 yaml 覆盖）
- 阈值降级要有**引导**（提示指向可用的替代视图）而不是裸报错——
  "图过大 → 用领域分组图/query relations"让用户有出路

### R34（2026-08-24）——AI 业务域分析（codeintel domains）+ 静态分析统一消费

**分析**：用户要求——让 AI 先分析出项目业务域，分析结果辅助后面静态
分析；**AI 介入前提供足够信息避免误判**；"不管输出类型，按相同划分，
只区分渲染形式，不区分数据源处理"。grilling 定案：单独子命令 +
wiki 内部调用（已生成过不再生成）、结构化事实包输入、写回 wiki.yaml
domains 区块（# AI 初稿 → 人工确认）。实施中用户追加两要求：① 测试
环境禁止真实 AI 调用；② 事实**导出到文件**再让 agent 读（不内联
prompt），并加导出命令。

**改进方案**：
- `codeintel domains`：静态事实包（包清单/表清单/实体/grpc+http 服务
  ——静态分析全算好）→ **导出事实文件**（默认 .codeintel/domain-facts.txt）
  → prompt 只含指令并引用文件路径（agent 先 Read 文件再归纳——信息
  充分性靠文件完整性）→ **校验**（归属包/表须在事实包中——AI 编造
  剔除+警告）→ 写回 wiki.yaml domains（# AI 初稿）
- `--export-facts <file>`：只导出事实包（不调 AI——可人工检查/喂任意
  agent）
- wiki 集成：yaml 无 domains 自动分析（幂等——已生成跳过；失败降级
  现有规则不阻断）；`CODEINTEL_SKIP_DOMAINS=1` 跳过
- **测试防 AI 双保险**：TestMain 注入拒绝 runner（agentRunner 覆盖——
  env 遗漏也快速失败而非真调 claude）+ env 开关
- 静态分析统一消费（用户"不区分数据源处理"原则）：splitERDomains/
  splitEntityDomains 都改 domains 优先（表→domains.tables、包→
  domains.packages 短名匹配），未覆盖走前缀/DDD 目录降级；渲染
  （md/html/plantuml）只做形式差异

**实施结果**：go2o 实测——AI 归纳 **8 个业务域**（商品域/交易履约域/
支付金融域/营销域/会员域/商户域/消息内容域/平台系统域），归属包表
全部合理（mm_→会员、mch_→商户、sale_→交易、pm_→营销），校验零
编造；wiki 重新生成 **幂等（0 次 AI 调用）**，ER 领域分组标题用 AI
中文域名（8 域），wiki-check 7/7；测试 TestParseDomainsValidate
（编造剔除）/TestParseDomainsFence/TestExportDomainFacts + 全仓
-race 13 包全绿。

**AI 杠杆点**（R34 实证）：
- 信息充分性 = 静态分析全算好 + 文件传递（prompt 只留指令）——
  大事实包内联 prompt 既占 token 又易截断；文件 + agent 读是正解
- 校验是 AI 产出的安全网（编造归属剔除）——AI 归纳可信但不可盲信
- 测试环境防真实 AI：runner 注入拒绝（快速失败）比 env 开关更根本
- "数据源统一、渲染分离"原则——领域归属一份数据，所有输出形态
  消费同一份，避免各页面规则分叉（R33 前缀 vs F2 目录的历史教训）

### R35（2026-08-24）——urfave/cli/v2 命令树识别（待办 3）

**分析**：待办 3——urfave/cli/v2 命令解析，不依赖具体文件路径。现状：
commands/processes 页已从 main 入口生成（R28/F1），但 urfave/cli 注册的
命令树（`cli.App{Commands: [...]}`）没被解析——命令清单页看不到真实
命令。grilling 定案（Q1-Q4 全 A）：独立命令 `query cli-routes` +
wiki commands 页消费；识别 App 字面量 Commands + 包级 Commands 变量
（v2 主流两形态）；递归一层 Subcommands；与 main 入口调用链并存
（补充不替换）。

**改进方案**：
- 构建期识别（markCLICommands——同 markGrpcServiceInterfaces 挂
  processPackage）：types 判断 urfave/cli/v2 的 App/[]*cli.Command
  类型 → 复合字面量提取 Name/Usage/Action/Subcommands（递归一层）→
  发射 cli_command 节点（cli_name/cli_usage/cli_action/cli_parent/
  register——嵌套父命令名拼接）
- 查询命令 `query cli-routes`：读节点 → 命令树 JSON（subcommands
  嵌套组织——parent 组装）
- wiki commands 页：有 cli 命令树先展示命令清单（缩进树），后接
  main 入口调用链

**实施结果**：测试 TestCLICommandTree（urfave stub via replace——App
字面量 + 包级变量 + 嵌套子命令 db.list 全识别）+ TestQueryCLIRoutes
（节点 → 树 JSON）全绿；全仓 -race 13 包全绿。go2o/ana 均未用
urfave/cli（识别机制由 fixture 验证，真实项目待观察）。

**AI 杠杆点**（R35 实证）：
- 识别模式复用：grpc 注册签名 → gin 路由 → urfave 命令树——同一
  "types 类型判断 + 复合字面量字段提取"模式，识别类功能模板化
- COALESCE 问题复发（NULL 属性 Scan 失败丢整行）——json_extract
  取可选属性必须 COALESCE（R34/R35 两次同坑，应进 runbook）

### R36（2026-08-24）——redis/kafka 外部依赖分析（待办 5）

**分析**：待办 5——redis client / kafka（sarama）调用分析。grilling
定案（Q1-Q4 全 A）：索引边/节点 + query external-deps 聚合 + wiki
消费；redis 方法式（go-redis）+ 命令式（conn.Do("GET", key)）都识别；
键名/topic 名提取（字面量+常量传播）→ redis_key/kafka_topic 节点 +
调用边；kafka producer/consumer 双向。

**改进方案**：
- ast 识别（emitRedisCall/emitKafkaCall——挂 emitSelectorCall）：
  redis 接收者类型判断（go-redis Named + **redigo 接口**——go2o 实测
  redis.Conn 是接口）+ 命令式 Do 第 1 参命令名/第 2 参键；键支持
  **跨包常量**（constants.QueueNewMailTask——types.Const 取值）；
  kafka SendMessage(&ProducerMessage{Topic}) / ConsumePartition(topic)
- `query external-deps`：redis 键（读/写/调用方/命令聚合）+ kafka
  topic（producer/consumer）JSON + 文本
- **嵌套调用修复**：`redis.Values(conn.Do(...))` 参数位置调用被
  isArgCall return 跳过（不建 calls 设计）——return 前补外部依赖识别
- wiki 消费：commands/外部依赖区块（external-deps 数据源）

**实施结果**：测试 TestRedisCallEdges（方法式+命令式+常量）/TestKafka
TopicEdges（producer/consumer 双向）全绿；**go2o 实测 4 个 redis 键**
（BLPOP 命令式：go2o:mq:mail/mm_update/order_notify/payment_success_
notify——跨包常量键名 + 调用方函数全识别）；全仓 -race 13 包全绿。

**AI 杠杆点**（R36 实证）：
- 外部依赖识别三坑：redigo 接口（Named 判断漏接口）、跨包常量
  （extractStringArg 只处理同函数）、嵌套调用（isArgCall return 跳过）
  ——"真实项目形态矩阵"比 fixture 多出三类，fixture 应主动覆盖
  嵌套/跨包/接口三形态（记忆：morphology-matrix-verification）
- redis 命令式（conn.Do("BLPOP", key)）是 go2o 主流——命令名+键
  参数模式比方法式更值得识别

### R37（2026-08-24）——系统流程基于 http/grpc 入口（待办 4，最后一个高优先）

用户定案（grilling）：发射端存 handler canonical ID（查询端无法解析
方法值短名）；gRPC 每方法一个入口；规模控制 = 上限 + 超出折叠（默认
15，--max-entries 可调）；**每 gRPC 服务独立子页**（md/html 双通道）；
main 节保留 + 新增路由节。

- **发射端 handler_id**（http_routes.go）：routeHandlerName → 返回
  (name, canonicalID)——函数 Ident（TypesInfo.ObjectOf）、方法值
  x.Method（x 包名 → 跨包函数 / x 变量 → (T).Method，解指针）、
  http.HandlerFunc(f) 递归、FuncLit 空。http_route properties 加
  handler_id；查询端 COALESCE 兜底（老索引 NULL 不丢行——R34/R35 教训）。
- **grpc ImplID**（query_grpc_routes.go）：grpcImpl 返回实现类型完整
  canonical ID（grpc_impl 边 source → implements 边追业务实现）；
  流程页按 `symbol:go:<pkg>:(Impl).Method` 构造方法入口（canonicalizer
  统一 (T).m 形态——直接拼字符串，勿忘括号）。
- **流程页三节**（wiki_processes_routes.go 新文件）：main 入口节
  （保留）→ HTTP 路由入口节（handler_id 展开、同 handler 多路由去重、
  resolver 分组 [native]/[gin]、匿名/方法值降级说明）→ gRPC 服务入口
  索引节（每服务子页链接）。折叠：超出上限只列清单（md 平铺 / html
  details），清单项也带子页链接。
- **gRPC 服务子页**（wiki_render.go / wiki_html_render.go）：每服务
  独立页（processes-grpc-<svc>.md/.html），页内每方法展开（协作子图 +
  时序图 + 涉及包）；HTML 复用 wikiHTMLPage 模板（返回总览链接）；
  cleanWikiOutDir 白名单加 processes-grpc- 前缀（动态文件名）。
- **--max-entries**（wiki.go）：流程页每节/每页入口展开上限，默认 15。
- **实测发现与修复**：
  - 多模块仓库重复发射（ana 8 个 go.mod 同一源码加载两次 → 路由
    重复）——httpProcEntries Paths 去重（routeLabel containsStr）。
  - **XxxServiceClient 客户端接口误伤**（R30-2 接口签名识别把客户端
    接口也当服务——go2o 62 子页 = 31 服务 + 31 Client）——ast 端排除
    Client 结尾接口，回落到 30 服务（与 R29 一致）。
  - **grpcRoutes(repo, "") 的 ServiceDesc 解析失效**——repoAbs 相对
    cwd 找不到 pb 文件 → wikiRenderCtx 加 RepoAbs 字段贯通（方法全集
    + handler 恢复正常）。
  - **SCIP 断言盲区**（核心补丁）：go2o 实现类仅靠
    `var _ proto.XxxServer = new(queryService)` 声明实现关系，
    scip-go 不输出 is_implementation → implements 边缺失 → grpc-impl
    追实现失败。新增 ast 端**编译期接口断言扫描**
    （ast_implements.go）：包级 `var _ Iface = expr` → 右侧动态类型
    具体类型（指针解包）→ types.Implements 指针/值方法集验证 → emit
    implements 边（接口 → 实现者，conf 0.8）+ 缺失端点节点补 emit
    （UPSERT 合并）。**通用补丁**（不只 grpc——任何断言实现的接口
    查询都受益）。go2o 实测补 43 条断言边。

**AI 杠杆点**（R37 实证）：
- scip-go 的 is_implementation 只覆盖显式实现关系——`var _ Iface =
  new(T)` 编译期断言不输出；AST 断言扫描是通用兜底（types.Implements
  精确验证，无误伤）。
- 内置接口（`var _ error = ...`）Named.Obj().Pkg() 为 nil——发射端
  补防护（go2o reindex 实测 panic 抓出）。
- canonical ID 拼接易错点：(T).m 的括号（ImplID + "." + m 拼错查不出
  ——方法解析静默失败，页面显示"无调用链"）。
- 验证用新二进制重跑（改代码后忘 go build——旧二进制跑 wiki 误判
  "去重没生效"，浪费一轮）。

### R38（2026-08-25）——gRPC 服务子页按领域分目录 + plantuml 边 label 修复

用户提出：① 每个领域创建二级目录放服务流程页；② wiki 图"线连接的是
数字根本看不懂"（plantuml PNG 实测）。

- **plantuml 边 label 语法 bug（根因）**：`A -->|6| B` 原转成
  `A --> 6 : B`——plantuml 把 6 当**目标节点名**（数字节点！），长符号
  ID 成了线标签。修正：`-->|N|` → 行尾冒号 label（`A --> B : 6`）。
  现有测试断言的是旧错误语法——一并修正（防回归）。
- **服务 → 领域映射**（用户定案：yaml 显式 + 调用链投票兜底）：
  - wikiDomainCfg 加 `services` 字段（AI 分析时归纳 + 人工确认，
    同 packages/tables 模式）；parseDomains 校验（服务名须在事实包
    svc 名单）；domainPrompt 强化"services 必填"
  - `serviceDomain(rc, svc)`：yaml services 精确匹配优先 → 方法调用链
    涉及包匹配 domains.packages 多数派投票 → 无匹配 "其他"
  - 投票实测教训：go2o 的 domains 把全部 infra 包（impl/service/query/
    parser）归入平台系统域——**投票天然偏向基础设施域**，静态兜底对
    go2o 无效；真正生效的是 yaml services（AI 归纳：ItemService→商品域、
    OrderService→交易域、MemberService→会员域……30 服务全分域无"其他"）
- **领域目录输出**：子页写 `<outDir>/<domain>/processes-grpc-<svc>.md
  /.html`（md/html 双通道；HTML 返回链接 ../index.html）；索引按领域
  分组（组内上限折叠，链接带目录路径）；cleanWikiOutDir 递归清领域
  目录（空目录移除——旧域不残留）
- **AI 读取仓库外文件被权限拒绝（根治）**：claude -p 对非 cwd 项目文件
  Read 弹窗无人应答即拒绝（R34 时用户在场批准过）。修复：agentRunner
  加 dir 参数——**agent 子进程 cwd = 目标仓库根**（cwd 项目内文件免权限，
  codex 同样受益）。ask/wiki--ai/domains 全部贯通。
- **domains 重跑两坑**：
  - 超时 240s→360s（任务加重：读 30KB 事实包 + 归纳 services）
  - yaml domains 新旧并存（setDomain 按名追加——go2o 实测 16 域 = 旧 8
    + 新 8）→ analyzeDomains 写回前 `clearDomains()`（整体重归纳语义）

**AI 杠杆点**（R38 实证）：
- plantuml 边 label 必须 `A --> B : label`——`--> label : B` 把 label
  当节点（渲染成数字节点），语法测试要断言正确形态而非旧形态
- 本地 CLI agent 的权限模型 = cwd 项目白名单——子进程 cwd 设为目标
  仓库根是通用解法（事实包/源码读取全免弹窗）
- 投票型静态兜底会被"基础设施兜底域"污染——显式配置（AI 归纳 +
  人工确认）才是可靠路径；兜底只当近似

## 待办与候选方向（未定优先级）

**高优先级待办**（2026-08-24 用户提出，6 项）：
- ~~1. HTTP/GRPC 路由自动分析~~ → **R29/R31 全完成**：grpc 部分
  （`query grpc-routes`——R29 注册签名识别 + R29-2 接口方法模式识别）；
  http 部分（`query http-routes`——R31 两个 resolver：原生 net/http
  [包级 Handle/HandleFunc + ServeMux 方法调用] + gin [路由方法 + Group
  前缀拼接/链式]，其他框架后置）
- ~~2. 图渲染双引擎~~ → **R32 已实现**（wiki `--diagram
  plantuml|mermaid`，默认 plantuml——HTML 渲染 PNG base64 嵌入、md
  输出 plantuml 文本块；mermaid 模式保持浏览器渲染）
- ~~3. urfave/cli/v2 命令解析支持~~ → **R35 已实现**（query
  cli-routes——命令树节点 + wiki 命令清单页）
- ~~4. 系统流程基于 http/grpc 分析出的入口~~ → **R37 已实现**：
  processes 页 = main 入口节（保留）+ HTTP 路由入口节（handler_id
  展开/去重/resolver 分组）+ gRPC 服务入口节（每服务独立子页，
  (Impl).Method 方法级展开）；上限折叠 --max-entries 默认 15
- ~~5. redis client / kafka（sarama）调用分析~~ → **R36 已实现**
  （query external-deps——redis 方法式+命令式、kafka producer/consumer）
- ~~6. grpc 枚举分析~~ → **R29 已实现**（.proto 源枚举并入 query
  enums，Source=proto 标注；生成代码 .pb.go 排除）

**交接遗留**（2026-08-24 交接文档 I §3 并入，随轮次更新状态）：
- ~~0. go2o AI 剩余缺口（31 表别名 + 283 列说明）~~ → **R28 已清零**
  （44 条补全、0 失败；150 表全有别名与列说明；wiki-check 7/7）
- 1. **--with-qa 实战未验证**：qa_history 需真实对话积累后 `wiki --ai
  --with-qa` 端到端生效（机制已测——TestWikiAIFillWithQA，待真实数据）
- 2. **表字段类型剩余 10 列**（repos 全局注册表，schema 在 ~/.codeintel
  ——yaml 补或读全局 db）
- 3. **术语表 24 条 / flows 5 条 review**（R11/R14 AI 初稿已入
  wiki.yaml，人工最后确认——wiki skill「人工是最后一道工序」）
- 4. **新人实测演练**：挑一个陌生项目，用 wiki 走通 onboarding 流程，
  验证"新人视角无死角"是否真实成立（覆盖度全勾选后终极验证，
  需外部项目）
- ~~5. ana 自身索引 update~~ → **R37 已 reindex**（含 R35-R37 分析逻辑；
  后续再改分析逻辑用 reindex——update 工作区干净会跳过）
- 6. **F2 实体分组对非 DDD 项目效果**（validSplit 降级逻辑已测；
  go2o 是 DDD 样例——普通项目待观察）

**R38 新增**：
- 1. **go2o domains.services 人工确认**：R38 重跑写回 30 个服务归属
  （AI 初稿——ItemService→商品域/OrderService→交易域等），维护者
  过目（git diff 可回滚）
- 2. **ana 自身 domains 补 services**：R37 分析的 7 域是旧格式（无
  services）——重跑 `codeintel domains` 后流程页服务归属生效（一次
  AI 调用）
- 3. 服务归属静态兜底改进（可选）：投票被基础设施兜底域污染
  （R38 实测）——可排除服务实现包再投票

**候选方向**：
- 流程页深度：入口调用链 → 关键数据流（value-trace 串联）
- ~~yaml 语义层：表列说明/表别名/模块描述 AI 初稿~~ → **R23 已实现**
  （`codeintel wiki --ai` 增量补缺，写回 wiki.yaml 标注 # AI 初稿）；
  ~~术语表接入~~ → **R24 已实现**（批量 prompt 带 glossary 区块）
- ~~wiki --ai 增强：列 prompt 加入表间关联事实（rels 已传入未用）~~ →
  **R24 已实现**（列说明带 rels 上下文）；~~ask 支持交互式 REPL~~ →
  **R26 已实现**（多轮追问复用上下文）
