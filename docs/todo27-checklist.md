# todo27 交付 Checklist（对照 `origin/main` 的 8da4ef9）

输入：`todo27.txt`（调用分析、流程续接与源码证据的剩余增量）。
规则：按编号独立验收；未完成项保留原因与剩余范围，不用"主要功能完成"代表整项。

## 状态总表

| # | 事项 | 状态 | 证据 / 剩余范围 |
| --- | --- | --- | --- |
| T01 | 局部闭包、到达定义与递归身份 | **已完成（本轮）** | `seqFuncValueAddrTaken`（`&f` 后回调绑定失效 → `func_value_unbound`）；`integration/todo27_t01_test.go`：Nested Hit=1 无假 cycle、Overwrite Hit=1/Miss=0、Captured Miss=1/Hit=0、Unused 不展开、Retried Hit=1、PtrEscape 无确定执行、Cycle 仍 `cycle` 停止；E-T01 逐字一致 |
| T02 | 命名函数列表的跨帧与到达定义 | **已完成（本轮）** | 纯内建（append/len/…）不再产生"未解析调用/回调逃逸"假诊断；命名 slice 类型/别名/方法值元素（`s.M`、`(&S{}).M`）/返回闭包；`Run(l...)` 多候选列表整体绑定 → 公共前缀确定 + 差异尾部互斥候选（`range 列表候选 i/N`）；`integration/todo27_t02_test.go` 逐项断言；E-T02 逐字一致 |
| T03 | 条件装饰器与跨帧捕获 | **已完成（本轮）** | 工厂条件返回按调用点实参/guard 裁剪（`seqReturnValuesOf` 语句路径 + `seqReturnGuardsAllow`；guard 子句后 `!cond` 可达性）；裸布尔条件（`if enabled`）参与场景判定；字面量展开键含**有界捕获环境**（`Use(Use(Hit))` 不再假 cycle）；捕获改写用新来源、未知改写不改回旧值；`integration/todo27_t03_test.go` |
| T04 | 未知返回、单例调用者与字段全部来源 | **已完成（本轮）** | 未知 return（nil/未解析调用/形参返回/退回到声明接口）→ 未知哨兵使唯一具体来源失效；类型转换（`API(&Two{})`、`(*T)(v)`）按实参来源解析（局部遮蔽正例）；工厂调用接收者（`FromField(1).Work()`）按 switch/if 守卫裁剪、未知实参保留 unknown 边界；`NewBoxX().Get().Work()` 字段证明按对象类型隔离（请求内缓存不串用）；`integration/todo27_t04_test.go`；非 init 条件写仍为 unknown 边界 |
| T05 | 返回实例跨方法、闭包与拷贝的来源一致性 | **已完成（本轮）** | 局部实例**顺序敏感到达定义**（`seqLocalInstanceFieldValue`）：调用前覆写/重复 setter 取最后一次、调用后写入不倒推、不同对象各自来源、取址逃逸/未知方法/条件写入失效；`localVarImpl` 改按 AST 对象身份 + 当前函数体（不借别的函数同名变量）；`curRecvFields` 沿方法调用传递；`integration/todo27_t05_test.go`；E-T05 工厂守卫正例保持 |
| T06 | DI 的准确容器身份与 AOP 守卫 | **未派发** | E-T06 正例逐字一致；剩余：动态/条件/非 init 注册、错误容器/key、AOP bypass 守卫与包装器 |
| T07 | 先解析调用点身份，再应用 depth | **已完成（本轮）** | 遮蔽导入别名按 AST 对象判方法身份；同行同名跨包调用各自准确 ID；depth=1 与完整展开身份逐条一致；单实现+未知实例不产生确定执行；修复 IIFE（Actor=`func(){…}`）整棵子树被渲染层丢弃（JSON/Mermaid 同一分析树）；`integration/todo27_t07_test.go` |
| T08 | 场景常量的源码身份与转换边界 | **已完成（本轮）** | `iota`/省略表达式与常量算术（`+ - * / % << >> & \| ^ &^`）进入同包/跨包/局部常量表（`seqConstSpecValues`）；局部 const 同时进字面量侧求值（`int32(local)`）；越界转换/被遮蔽 `int32`/未知左值短路保持 unknown；`integration/todo27_t08_test.go`；E-T08 逐字一致 |
| T09 | go-micro 生成桩源码回退与准确路由 | **已完成（本轮）** | 旧索引（缺 `rpc_service`/`registers_service` 属性与 `grpc_impl` 边）→ 源码回退：`Register<Handler>Handler` 的真实调用边 + 注册实参类型断言 + 方法体唯一返回收窄（多实现不猜）；未被调用的 helper 不是路由；同形客户端/重复调用按调用点身份；`integration/todo27_t09_test.go`（含旧索引 DB 变更 + 反例） |
| T10 | 外部形参的词法回调与列表重绑定 | **未派发** | E-T10 正例逐字一致；剩余：外部形参普通参数/命名列表/ellipsis/返回闭包/创建处 receiver、同名 process/f 不继承 |
| T12 | 在原调用上下文同步续查 | **部分**（候选不续查） | 接口根入口候选子树 `candidate=true` 且不登记续查前沿；剩余：词法回调/返回实例字段/RPC/HTTP 前沿的快照续接、跨仓 reader 生命周期、Continue{MaxSegments} 计数口径 |
| T14 | 运行时输入兼容与严格来源边界 | **未派发** | E-T14 逐字一致（`rewrite(&enabled)` 后两分支保留）；剩余：已解析 RuntimeData vs 字符串路径、typed-nil、同名/初始化 `*sql.DB`、CTE/限定名拒绝、Scan 列全量校验 |
| T18 | 仅补充回归、兼容声明和文档 | **部分**（本轮同步文档） | 新增 U06 回归；`docs/field_trace.md §175`、本 checklist、AGENTS 教训；剩余：AdapterTimeout 兼容回归、分批/写缓存/边图/SSA 缓存/typeString 缓存回归、Linux-only inotify、预算采样边界、帮助/JSON 契约同步 |
| U04 | 跨入口新审核节点与多次 Finish | **未派发** | E-U04 持久化边界输出保持（`INSERT INTO nodes` + 未证明顺序）；剩余：跨入口请求身份/守卫证明、21–28 八个同步流程形态 |
| U06 | 未知实例来源的剩余入口与续查缺口 | **已完成（本轮）** | 见下节 |

## U06 验收明细（已完成）

生产链路：源码 → `init` → CLI（`integration/todo27_u06_test.go`，2 个测试函数）：

- 未知（depth=1 与 depth=12 同口径，`unknown_receiver`，无确定 `Hit`）：
  - `(Getter).Entry`：`s.Get().Work()` —— getter 返回 receiver 字段（`s.biz`）无写入证明；
  - `(Mixed).Entry`：构造器写 `&One{}` + setter 写形参 → 写者来源不完整；
  - `Parameter`：接口形参；
  - `Local`：形参别名（`x := v`）；
  - `Singleton`：包级单例存在非 init 调用者（`UnknownInject`）。
- 接口根入口 `(API).Work`：实现只作为未验证候选（`iface_ambiguous`
  + `interface-entry`，JSON `"candidate": true`，后代不计确定执行/不续查）。
- 正例仍确定执行：`KnownEntry`（构造器字段字面量）、`KnownLocal`
  （`var v API = &One{}`）`Hit=1`。
- 兼容回归：E-T01…E-T14 的 `预期输出原文示例` 逐字一致（12 个独立
  fixture，真实 init 后 mermaid 输出 diff 为空）；`make it` 全量通过
  （含 `TestTodo25WorkspaceIfaceRepoIsolation` 跨仓隔离）。

### 实际执行

```sh
scripts/verify.sh --quick      # build + vet + 非 race 全量单测：通过
make it                        # integration（scip-go 在 PATH）：通过，70s
go test -tags integration ./integration -run 'TestU06|TestTodo25WorkspaceIfaceRepoIsolation' -count=1
```

### 未完成边界

- 形参实例来源只证明"全部已索引调用点同一具体来源"（`GetCallers` ≤16）；
  动态/跨仓调用仍未知。
- 接口根入口候选子树深度沿用候选预算（≤2 层），不续查。
