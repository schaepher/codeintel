package domain

// R9 实体协作图：把函数级调用链抽象为对象实体间的交互——
// 类型（struct/interface）为实体，游离函数按包聚合为「包门面」
// 实体；实体间边 = 方法互调聚合计数，实体内交互 = 节点内互调
// 计数。输出 4 类设计诊断（高耦合对/循环依赖/上帝对象/游离函数
// 占比）——反向优化项目自身设计。

// EntityNode 一个协作实体（类型或包门面）。
type EntityNode struct {
	ID          string `json:"id"`           // symbol:go:<pkg>:<Type> 或 :<pkg>（门面）
	Name        string `json:"name"`         // 短名（类型名 / 包名）
	Pkg         string `json:"pkg"`          // 所属包路径
	Kind        string `json:"kind"`         // struct / interface / pkg-face
	MethodCount int    `json:"method_count"` // 类型方法数（门面 = 0）
	FreeFuncs   int    `json:"free_funcs"`   // 门面聚合的游离函数数（类型 = 0）
	InnerCalls  int    `json:"inner_calls"`  // 实体内方法互调次数
	OutCalls    int    `json:"out_calls"`    // 出边调用总数（聚合计数）
}

// EntityEdge 实体间调用边（方法互调聚合计数）。
type EntityEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// EntityDiag 设计诊断条目。
type EntityDiag struct {
	Kind   string `json:"kind"` // coupled / cycle / god-object / face-heavy
	Target string `json:"target"`
	Detail string `json:"detail"`
}

// EntityGraph 实体协作图（节点 + 边 + 诊断）。
type EntityGraph struct {
	Nodes []*EntityNode `json:"nodes"`
	Edges []*EntityEdge `json:"edges"`
	Diags []*EntityDiag `json:"diags"`
	// EntityOf 函数/方法 canonical ID → 实体 ID（渲染层映射用）。
	EntityOf map[string]string `json:"-"`
	// ByName 符号短名 → 实体 ID 列表（流程页把函数级调用链的短名
	// 映射为涉及实体——短名跨包可能重名，故为列表）。
	ByName map[string][]string `json:"-"`
}

// EntityRaw 实体聚合原始数据（repo 层一次性读取，action 层聚合）。
type EntityRaw struct {
	Types   []*CodeEntity // struct/interface 节点
	Funcs   []*CodeEntity // function 节点（游离函数）
	Methods []*CodeEntity // method 节点（R66：接口方法统计——has_method 不覆盖接口）
	HasM    []*Fact       // has_method 边（类型 → 方法）
	Calls   []*Fact       // 全量 calls 边
}

// 实体诊断阈值（Q6：固定起步，自举首份报告后按实际分布调整）。
const (
	EntityKindStruct  = "struct"
	EntityKindIface   = "interface"
	EntityKindPkgFace = "pkg-face"

	DiagCoupledMin   = 20 // 高耦合对：跨包实体间方法互调 ≥ 20 次
	DiagGodMethods   = 40 // 上帝对象：方法数 ≥ 40
	DiagGodOutCalls  = 20 // 上帝对象：出边 ≥ 20（小类型调得广不误报）
	FaceMinFreeFuncs = 5  // 门面实体：包游离函数 ≥ 5 才建
	DiagFaceHeavyMin = 1  // 游离函数占比：游离函数数 > 包方法总数时提示

	DiagCoupled    = "coupled"
	DiagCycle      = "cycle"
	DiagGodObject  = "god-object"
	DiagFaceHeavy  = "face-heavy"

	// EntityMinEdgeCount 概览全图弱关联边过滤阈值（R16）：方法互调
	// 次数 < 阈值的边不画、其孤立实体隐藏——全图聚焦真实协作。
	EntityMinEdgeCount = 3
)
