package domain

// R100 wiki 源码事实类型（迁移自 cli——wiki 数据源进 action 后跨层
// 共享）：ORM 结构体扫描（R20）与 HTTP API 路由解析（R1）结果。

// ORMStruct 一个与表关联的结构体（TableName() 方法反查——源码事实）。
type ORMStruct struct {
	Name   string     `json:"name"`
	File   string     `json:"file"`
	Line   int        `json:"line"`
	Code   string     `json:"code"` // 结构体定义源码片段
	Fields []ORMField `json:"fields,omitempty"`
}

// ORMField 结构体字段（R21：Go 类型 → 表列类型 fallback；R22：字段
// 顺序 + 自增识别）。
type ORMField struct {
	Name      string // 字段名
	GoType    string // Go 类型（int64/string/time.Time…）
	Column    string // 列名（gorm column tag 优先，无 tag snake_case）
	IsAutoInc bool   // gorm tag 含 autoIncrement
}

// APIRoute 一条 HTTP 路由（目标仓库 internal/server 源码解析——wiki
// api 页数据源）。
type APIRoute struct {
	Method  string // GET/POST/DELETE/任意（路由行注释补充）
	Path    string
	Handler string
	Desc    string
}
