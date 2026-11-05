# todo30 交付 Checklist（输入：`todo30.txt`，基线 `origin/main` 9a3f29a）

规则：按编号独立验收；未完成项保留原因与剩余范围，不用"主要功能完成"代表整项。
生产链路一律 **源码 → init → CLI**（`integration/todo30_*_test.go`）。

## 状态总表

| # | 事项 | 状态 | 证据 / 剩余范围 |
| --- | --- | --- | --- |
| U04a | 关联入口选择、统一分析与共享预算 | **已完成** | `CodeSequenceRequest.RelatedEntries` + CLI 可重复 `--related-entry`；**先校验全部入口**（准确 canonical `symbol:go:<pkg>:<name>` + 当前仓库精确存在 + kind=function/method），再执行分析；重复入口只分析一次、根入口外按 canonical ID 排序、上限 32（含根）；每入口独立环境/防环路径，共享 `req.acc`（取消 + 追加预算）与 pkgSet；`workflow.entries{symbol,repo,sequence}` + `ordering=unproven` + 集合级 `workflow.continuation`；文本/Mermaid 标注 `entry order unproven`。测试：`integration/todo30_u04a_test.go`（entries/去重/排序/拒绝非法入口/共享预算/单入口输出不变） |
| U04c | 分支比较操作数的结构化来源 | **已完成** | branch 节点 `condition{operator,left,right,terms,unknown,expression}`；支持 `== != < <= > >=`、`&& || !`、裸布尔；操作数用 `SeqStorageOrigin`（参数/字面量/字段读取/返回槽/拼接，环境覆盖与取址逃逸照旧）；未解析操作数省略对应侧 + `operand_origin_unproven`；上限 64 节点/16 层，超限整项 `operator=unknown`+`condition_budget`；**不改变 Scene 判定**。测试：`integration/todo30_u04c_test.go`（参数/覆盖/len/取址/逻辑嵌套/预算） |
| U07b | 关联入口中同名参与者的独立身份 | **已完成（渲染层）** | 关联入口 Mermaid 参与者按入口作用域隔离：P0 `External entries` + 每个入口自身/局部对象独立 Pn；短名冲突时入口自身用 canonical ID、局部对象用 `<入口ID>.<actor>`；类型 `<br/>` 保留；JSON 原树 Actor/Nodes/Else 不被渲染修改。测试：`integration/todo30_u07b_test.go`（alpha/beta 同名 Run/db 逐行断言 + 单入口不变） |
| U04b | 资源、记录键与条件化读写连接 | **未开始** | 依赖 U04a（已就绪）。需要：按准确资源/表/全部 AND 等式匹配可信读写、条件真假与可达 Else、`workflow.connections`/`keys`/`read_values`/`missing_proofs`、4096/256 预算。当前 `storage_read`/`storage` 证据已存在但无跨入口匹配与键作用域。 |
| U04d | 标准 SQL 读取结果身份及返回槽传播 | **已完成** | `storage_read.result{id,repo,entry,symbol,file,offset,slot,type}`（QueryRow→Row / Query→Rows；每次求值 `read-N` 独立）；标量来源 `read_result` 沿 `row := …`/别名/return 槽传播（`rows, err := Query` 绑定槽 0）；覆盖/`nil`/取址逃逸/星号投影/自定义同名方法不产生或恢复身份；错误槽保持未知。测试：`integration/todo30_u04d_test.go`（ReadNode/覆盖/取址/星号/自定义/两次调用不同身份/depth1+续查） |
| U04e | SQL Scan 列来源、错误身份及成功前提 | **已完成** | `evidence.scan{id,read,success_conditions,targets,unsupported,outcome}`；按 SELECT 真实列序绑定 `&scalar`，列来源 `read_column{result=Scan,column,index,when=scan_success}`；`scan_error` 来源（同一 scan/read 身份）+ if-init 错误变量进入条件操作数（nil 操作数给 `kind=nil`）；Rows 保留 `query_success/rows_next_true/scan_success`；列数不符/目标不支持清掉旧值、Scan 前取址逃逸持续失效、defer/自定义 Scanner/同名方法不借用。测试：`integration/todo30_u04e_test.go`（正例/Rows/辅助函数+别名/覆盖/列数不符/逃逸/defer/自定义/depth1+续查） |
| U04f | 从条件连接生成独立请求实例 | **未开始** | 依赖 U04b；需要 `workflow.instances`（selected_root/conditional_external_request）、bindings/参数作用域、read_values 代入分支、实例树复制（64 实例/8 层/65536 节点）、`partial`/`instance_boundaries`、取消即失败。 |
| U04g | 条件化请求实例用于文本与时序图 | **未开始** | 依赖 U04f；需要同一展示树输出文本/Mermaid/SVG/PNG、selected root / conditional external request / instance id 标注、条件块与 missing_proofs、Scan 错误回外部调用方。 |
| U04h | 同一源写入的多个读取联合约束 | **未开始** | 依赖 U04b/U04e；需要 `related_reads[]`（独立 read/connection_index/read_values）、各 Scan 独立成功守卫、`joint_read_values_preserved`。 |
| U04i | 嵌套字段/getter 使用准确字段值类型 | **已完成** | `SeqStorageFieldRead.valueRef`（内部缓存，不进 JSON）保存字段**值类型**；`req.Child.ID`/`child.ID`/`req.Ptr.ID`/`req.GetChild().ID`/`req.Child.GetID()` 的 `type_id` 均为字段值类型 `Leaf`，路径保留 `Child.ID`/`Ptr.ID`；缓存缺失时从 owner+最后一级字段声明恢复；类型别名按真实声明解析（`type Leaf = model.Leaf` → `model.Leaf`/`model/model.go`），新定义类型（`type Leaf model.Leaf`）不借用方法集合；请求根取址失效与 getter 返回非接收者对象保持未知。测试：`integration/todo30_u04i_test.go` |
| U04j | 标准 SQL Scan 连接值类型模型直接标量字段 | **已完成** | `env.fields` 字段槽（按对象声明+字段/分配+字段隔离；分支/循环合并缺失即未知）；`&record.Kind` 按 SELECT 列序绑定 `read_column`；预算 256 槽/256 根/4096 别名/4096 归属 → `scan_field_budget`；普通字段写入、整对象覆写、`*=...`、自增、未证明只读的方法调用/接收者、捕获写者、go/defer 调度体读取均失效；类型别名/指针根无分配证明/嵌套路径/自定义 Scanner 不支持。测试：`integration/todo30_field_slots_test.go` |
| U04k | 分配证明连接指针模型及局部别名 | **已完成** | `new(T)` → `allocation{id,type_id,repo,entry,symbol,file,offset}`（别名共享同一 ID；重绑只切分配不清旧别名；`*alias=...`/字段写/传出/全局/通道/未知调用/方法接收者使分配失效；跨入口/仓库不借用）。测试：`todo30_field_slots_test.go` |
| U04l | 通过静态只读 getter 保留当前 Scan 字段来源 | **已完成** | 零参单 `return receiver.Field` + 内建标量声明一致 → 调用方对象字段槽来源（任意方法名/跨包）；修改方法/返回其它对象/带实参保持保守；defer 实参立即捕获保留、延迟闭包体不借用。`getter` 返回槽在求值时刻快照（后续字段变化不补历史返回值）。测试：`todo30_field_slots_test.go` |
| U04 | 跨入口同记录、成功写入与多次 Finish 整合 | **未开始** | 依赖 U04b/U04f/U04g；需要资源/键/过滤/成功路径/请求身份与条件化 Mermaid（含"同一 FinishReview 两次独立请求"）。本轮只完成入口集合与条件操作数基础。 |
| B01 | getter 实际返回值贯通立即执行匿名函数 | **已完成（公开示例）** | `return getter()` 在返回求值时刻固定来源（`snapshot` 不被续查后序重算覆盖），经 IIFE 返回槽传给外层调用（`defer Take(func(){return record.Current()}())` → `Take` 的 `kind=="profile"` 得到 `read_column/kind`）；deferred 闭包体读取保持未知。测试：`todo30_field_slots_test.go::Immediate/DeferBody` |
| B02 | 跨构造函数及只读调用传递模型所有权与字段值 | **未开始** | 依赖 B01；需要构造返回分配身份 + 被调方法体"不修改/不保留"证明（不能把任意指针传参当完成方案）。 |
| B03 | 嵌套字段/类型别名/ORM 模型到列映射 | **未开始** | 依赖 U04i/U04j；SQL 嵌套路径 + XORM 准确模型/表/列/读取结果接入同一机制（显式 Table + 列标签 → TableName + 命名策略）。 |
| B04 | 联合不同写入提供的读取条件 | **未开始** | 依赖 U04h/B03；同表不同写入按可达顺序计算覆盖，不混历史值与最终值。 |
| B05 | 数据库生成键及准确错误身份 | **未开始** | 需要 `Result.LastInsertId` 生成键来源（`origin.kind=generated_key`）、原 Exec 返回槽 + 两项独立成功前提；ORM 自动主键回填需框架效果证明。 |
| B06 | 事务资源连接与提交可见性 | **未开始** | 需要事务写入连接到 `Begin` 的底层资源、`transaction_commit` 缺失证明、失败/Rollback/条件提交/未知归属保留。现有 `tx_origin/tx_error` 可复用但未与跨入口连接结合。 |
| B07 | 用条件实例新事实恢复已证明调用前沿 | **未开始** | 依赖 U04f/U04l；实例列值参与工厂/接口/回调目标重解析并续接未展开函数体（无准确实例来源仍不得唯一实现回退）。 |
| B08 | 关联入口及派生实例中保留运行时事实范围 | **未开始** | 需要每入口独立接收同一不可变快照/资源绑定、实例条件参数查询快照、`missing_fact`/`analysis_boundary`/`ambiguous_fact` 分类。 |
| B09 | 前端操作接入已证明请求入口 | **未开始** | 需要 `FrontendRoots []string` + 可重复 `--frontend-root`、TS/TSX 方法/路径/包装函数/事件绑定解析、`workflow.frontend.actions`（源码位置/方法/路径/入口/参数）。 |
| B10 | 自动选择合法组合并验收完整业务覆盖 | **未开始** | 依赖 B09；需要 `CombinationSelector` + `--combination-selector`、`workflow.coverage{supported,analyzed,complete,partial}`；真实 28 流程属调用方接入验收。 |
| B11 | 一次命令导出全组合 Markdown 与时序图 | **未开始** | 依赖 B10；需要 `sequence-batch --manifest --out --json`（复用 action 结果与 CLI 渲染树）、失败隔离/非零退出/稳定路径与顺序。 |

## 本轮已执行验证

```sh
scripts/verify.sh --quick                         # OK（build+vet+非 race 全量单测）
go test ./internal/action ./internal/cli -count=1 # OK
go test -tags integration ./integration -run 'TestU30' -count=1  # OK
```

- 真实仓库验收（源码→init→CLI）：
  - U04c：`example.com/condition` —— `Entry/Overwrite/Length/Escape/Nested/Deep`；
    `id=="profile"` 输出 `left.kind=parameter, parameter=0, scalar_type=string`、
    `right.literal="\"profile\""`；`len(id)>0` left 省略 + right 整数 0 +
    `operand_origin_unproven`；20 层逻辑嵌套 + 21 项 `&&` → `condition_budget`。
  - U04a：`example.com/flow` —— `Submit --related-entry FinishReview` →
    `{"entries":2,"ordering":"unproven"}`；重复/含根重复去重后 3 个入口按
    canonical ID 排序；非法/不存在/用错命令非零退出且无半份 JSON；
    `--depth 1 --continue-segments 1` 集合 `expanded≤1`。
  - U04d：`example.com/flow:ReadNode` —— `return alias` 的
    `value_origins.read_result = {id:read-1,type:Row,symbol:(DB).QueryRowContext,
    slot:0}`；覆盖/取址/星号/自定义不保留身份；辅助函数两次调用得到 read-1/read-2。
  - U04e：`example.com/scanx` —— `if err := …Scan(&kind); err != nil` 的
    `err!=nil` left=`scan_error{scan-1,read-1}`、`kind=="profile"` left=
    `read_column{result.id=scan-1,result.read.id=read-1,column=kind,
    when=scan_success}`；Rows 的 success_conditions 三项齐全；其余反例见测试。
  - U04i：`example.com/nested` —— Entry 5 个 branch 的 `field_read.type_id`
    全部为 `Leaf`（`Child.ID`/`Ptr.ID` 路径保留）；`Escaped`/`WrongGetter` 左
    操作数未知；类型别名解析到 `model.Leaf`（源码 `model/model.go`），新定义
    类型不借用 `model.Leaf.GetID`。
  - U07b：`example.com/flow/alpha:Run` + `beta:Run` 参与者逐行等于契约
    （P0 External entries / P1 canonical alpha.Run / P2 alpha.Run.db<br/>alpha.DB /
    P3 canonical beta.Run / P4 beta.Run.db<br/>beta.DB）。
- 跳过：`make e2e`（本轮无前端改动）；`make it` 全量（仅跑本轮新增集成测试；
  提交前会跑全量）。

## 实现依赖与下一轮顺序

```
U04a(完成) → U07b(完成)
U04a(完成) → U04b → U04f → U04g → U04
U04b → U04d(完成) → U04e(完成) → U04j(完成) → U04k(完成) → U04l(完成) → B01(完成) → B02 → B03 → B07
U04e → U04h → B04
U04i(完成) → B03
B05/B06 独立于扫描字段链，但跨入口连接需 U04b
B08 依赖 U04f + 运行时快照；B09 → B10 → B11
```

下一轮从 **U04b/U04f/U04g** 开始（`scan_field`/`read_column`/getter 链路已
就绪，可完成 U04l 的 workflow.instances 公开命令），再到 U04h/U04 与 B02–B11。

## 已知边界（本轮有意保留）

- `workflow.entries` 只表示"选择了哪些入口"：不提供步骤、顺序、次数或记录键
  映射；`ordering=unproven`，入口数量不等于请求/人工操作次数。
- 关联入口集合展示在**渲染层**做参与者作用域隔离；JSON 原树的 Actor 仍是源码
  短名（渲染动作不修改分析结果）。
- 每个入口共享 `req.acc`：`workflow.continuation` 是集合级统计；各入口
  `meta.continuation` 仍按其返回时刻的累计值输出（不重算为单入口口径）。
- U04c 只记录来源，不改变 Scene 判定；`condition` 的预算按分支整项计算
  （超限不保留部分逻辑表达式）。
- `--related-entry` 的 canonical 校验要求 `symbol:go:` 前缀（Go 仓库）；跨语言
  canonical ID 不在本轮范围。
