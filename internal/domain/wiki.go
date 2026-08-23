package domain

// #238 wiki 生成数据：模块页六区块（职责/入口/核心符号/模块间调用/
// 相关表）。WikiData 由 action 聚合，cli 渲染 Markdown。

// WikiModule 一个模块的 wiki 数据。
type WikiModule struct {
	Name        string       `json:"name"`                 // module 路径（go.mod module 名）
	ShortName   string       `json:"short_name"`           // 路径末段（页面文件名）
	Desc        string       `json:"desc,omitempty"`       // 包注释（职责）
	Entries     []string     `json:"entries,omitempty"`    // 入口符号名（main/服务）
	CoreSymbols []*WikiSymbol `json:"core_symbols"`        // 核心符号（callers 降序 Top N）
	OutCalls    []string     `json:"out_calls,omitempty"`  // 调用的模块
	InCalls     []string     `json:"in_calls,omitempty"`   // 被哪些模块调用
	Tables      []string      `json:"tables,omitempty"`     // 相关表（该模块代码写入的表）
	Flows       []WikiFlow    `json:"flows,omitempty"`      // 自动时序（每个一级调用分支单独一个图）
	PkgCalls    []*WikiPkgCall `json:"pkg_calls,omitempty"` // 模块内包间调用（Q251-A：模块页架构图）
}

// WikiPkgCall 模块内一条包间调用（Q251-A）：调用方包 → 被调包 + 次数。
type WikiPkgCall struct {
	From  string `json:"from"`  // 调用方包短名
	To    string `json:"to"`    // 被调包短名
	Count int    `json:"count"` // 调用次数
}

// WikiFlow 一条流程的时序数据（单独画一张 sequenceDiagram）。
type WikiFlow struct {
	Title string         `json:"title"`           // 流程名（一级被调者短名）
	Steps []WikiSeqStep  `json:"steps,omitempty"` // 调用步骤（caller → callee）
}

// WikiSeqStep 时序图一步（调用者 → 被调用者短名）。
type WikiSeqStep struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
}

// WikiSymbol 核心符号条目。
type WikiSymbol struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Callers int    `json:"callers"`
	File    string `json:"file"`
	Line    int    `json:"line"`
}
