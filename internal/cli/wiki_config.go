package cli

// wikiModuleCfg 模块配置（命名类型——wiki --ai 补缺时追加/更新）。
type wikiModuleCfg struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Order       int    `yaml:"order"`
}

// wikiDomainCfg 业务域（R34：AI 基于代码事实归纳——名称/描述/归属包与
// 表；静态分析（ER 领域分组/实体分组）统一消费此数据源，未覆盖的
// 包/表走前缀/DDD 目录规则降级）。
type wikiDomainCfg struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Packages    []string `yaml:"packages"`
	Tables      []string `yaml:"tables"`
}

// wikiConfig wiki.yaml 契约（AI 产出 → 人工最后确认微调）。
type wikiConfig struct {
	Project struct {
		Description string `yaml:"description"`
	} `yaml:"project"`
	Modules []wikiModuleCfg `yaml:"modules"`
	Tables        []wikiTableConfig `yaml:"tables"`
	HiddenSymbols []string          `yaml:"hidden_symbols"`
	// 架构图（mermaid 代码块；为空时自动从模块间调用生成）
	Architecture string `yaml:"architecture"`
	// 业务流程时序（业务语义，代码画不出——维护者模式访谈产出）
	Flows []struct {
		Title   string `yaml:"title"`
		Mermaid string `yaml:"mermaid"`
	} `yaml:"flows"`
	// 术语表（#246 业务黑话解释：ssa/ast/ER 等；--ai 可从事实识别补缺）
	Glossary []wikiGlossaryItem `yaml:"glossary"`
	// 业务域（R34：AI 分析产出 → 人工确认；ER/实体分组统一消费）
	Domains []wikiDomainCfg `yaml:"domains"`
}

// wikiGlossaryItem 术语条目（命名类型——wiki --ai 补缺时追加/更新）。
type wikiGlossaryItem struct {
	Term       string `yaml:"term"`
	Definition string `yaml:"definition"`
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
