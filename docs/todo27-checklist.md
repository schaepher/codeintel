# todo27 交付 Checklist（对照 `origin/main` 的 8da4ef9）

输入：`todo27.txt`（调用分析、流程续接与源码证据的剩余增量）。
规则：按编号独立验收；未完成项保留原因与剩余范围，不用"主要功能完成"代表整项。

## 状态总表

| # | 事项 | 状态 | 证据 / 剩余范围 |
| --- | --- | --- | --- |
| T01 | 局部闭包、到达定义与递归身份 | **未派发** | 现有 defer/go 求值顺序正例保持（E-T01 逐字一致）；剩余：retry/多层实际回调用词法身份、覆盖/取址/捕获改写失效、递归键含词法回调与有界捕获环境 |
| T02 | 命名函数列表的跨帧与到达定义 | **未派发** | E-T02 正例逐字一致；剩余：命名 slice/别名、导入包类型源码解析、列表经返回闭包/方法值/外部形参、遮蔽/兄弟分支 append |
| T03 | 条件装饰器与跨帧捕获 | **未派发** | E-T03 正例逐字一致；剩余：条件返回原回调/包装闭包、嵌套工厂、同名回调跨多层 helper 的到达定义 |
| T04 | 未知返回、单例调用者与字段全部来源 | **部分**（单例未知写者已随 U06 修） | `seqParamCallTypes` 非 init 调用者 → 未知哨兵（Singleton 反例）；剩余：局部别名/跨包变量全部来源、任一未知 return 使具体证明失效、跨包 init 调用者实参 |
| T05 | 返回实例跨方法、闭包与拷贝的来源一致性 | **部分**（字段写者不完整已随 U06 修） | 混合写者（构造器 + setter 形参）→ `unknown_receiver`；剩余：值拷贝/指针逃逸、调用后修改不倒推、嵌入方法最近 owner、同深度多路径 |
| T06 | DI 的准确容器身份与 AOP 守卫 | **未派发** | E-T06 正例逐字一致；剩余：动态/条件/非 init 注册、错误容器/key、AOP bypass 守卫与包装器 |
| T07 | 先解析调用点身份，再应用 depth | **部分**（接口派发顺序已校） | 实例来源闸门在 `InterfaceMethodImpl` 唯一实现之前，depth=1 与展开路径同口径；剩余：旧同行汇总边名称/包核对、遮蔽导入别名、同行同名跨包 |
| T08 | 场景常量的源码身份与转换边界 | **未派发** | E-T08 正例逐字一致（`0x4` → 4 已归一化）；剩余：同包/跨包常量、iota、别名遮蔽、转换范围检查 |
| T09 | go-micro 生成桩源码回退与准确路由 | **未派发** | E-T09 正例逐字一致；剩余：客户端 receiver 字段、NewRequest/Call 源码回退、旧索引缺注册属性发现、同服务重复调用身份对照 |
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
