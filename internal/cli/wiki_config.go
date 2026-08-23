package cli

// wikiConfig wiki.yaml 契约（AI 产出 → 人工最后确认微调）。
type wikiConfig struct {
	Project struct {
		Description string `yaml:"description"`
	} `yaml:"project"`
	Modules []struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Order       int    `yaml:"order"`
	} `yaml:"modules"`
	Tables        []wikiTableConfig `yaml:"tables"`
	HiddenSymbols []string          `yaml:"hidden_symbols"`
	// 架构图（mermaid 代码块；为空时自动从模块间调用生成）
	Architecture string `yaml:"architecture"`
	// 业务流程时序（业务语义，代码画不出——维护者模式访谈产出）
	Flows []struct {
		Title   string `yaml:"title"`
		Mermaid string `yaml:"mermaid"`
	} `yaml:"flows"`
	// 术语表（#246 业务黑话解释：ssa/ast/ER 等）
	Glossary []struct {
		Term       string `yaml:"term"`
		Definition string `yaml:"definition"`
	} `yaml:"glossary"`
}

// wikiTableConfig 表结构契约（#243 表详情：字段定义/索引/建表语句——
// 业务表 schema 在外部库，代码分析不出，AI 调研产出 + 人工确认）。
// wikiTableColumn 表列配置（yaml tables.columns；Hidden 为 R3 列级
// 噪音隐藏——解析噪音列等不渲染）。
type wikiTableColumn struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	Default string `yaml:"default"`
	Comment string `yaml:"comment"`
	Hidden  bool   `yaml:"hidden"`
}

type wikiTableConfig struct {
	Name    string            `yaml:"name"`
	Alias   string            `yaml:"alias"`
	Hidden  bool              `yaml:"hidden"` // #245 噪音表隐藏（fixture 等）
	Columns []wikiTableColumn `yaml:"columns"`
	Indexes []string          `yaml:"indexes"`
	DDL     string            `yaml:"ddl"`
}

// wikiMeta 渲染用的模块增强信息（yaml 合并结果）。
type wikiMeta struct {
	desc  string
	order int
}
