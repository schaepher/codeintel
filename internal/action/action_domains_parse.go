package action

// R34 domains 解析/校验/AI prompt（批次 C 迁移，原 cli/domains.go +
// domains_sub.go 的纯函数部分）：AI 返回 YAML 解析 + 归属校验（防编造）
// + subdomains 清洗；prompt 构造；事实包 JSON 序列化。

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseDomains 解析 AI 返回的 domains YAML + 校验（归属须在事实包中，
// 防 AI 编造——校验失败剔除并警告）。
func ParseDomains(resp string, f *DomainFacts) ([]WikiDomainCfg, []string) {
	var out struct {
		Domains []WikiDomainCfg `yaml:"domains"`
	}
	if err := yaml.Unmarshal([]byte(resp), &out); err != nil {
		// 宽松重试（可能带 ```yaml 围栏）
		s := StripYAMLFence(resp)
		if err2 := yaml.Unmarshal([]byte(s), &out); err2 != nil {
			return nil, []string{fmt.Sprintf("domains 解析失败: %v", err2)}
		}
	}
	havePkg := map[string]bool{}
	haveTbl := map[string]bool{}
	haveSvc := map[string]bool{}
	for _, p := range f.Pkgs {
		havePkg[p.Path] = true // 完整路径校验（AI 输出完整路径）
	}
	for _, t := range f.Tables {
		haveTbl[t.Name] = true
	}
	for _, s := range f.Svcs {
		haveSvc[s.Name] = true
	}
	var doms []WikiDomainCfg
	var warns []string
	for _, d := range out.Domains {
		if d.Name == "" {
			warns = append(warns, "跳过无名称的域")
			continue
		}
		var pkgs, tbls, svcs []string
		for _, p := range d.Packages {
			if havePkg[p] {
				pkgs = append(pkgs, p)
			} else {
				warns = append(warns, fmt.Sprintf("域 %s：包 %s 不在事实包中（剔除）", d.Name, p))
			}
		}
		for _, t := range d.Tables {
			if haveTbl[t] {
				tbls = append(tbls, t)
			} else {
				warns = append(warns, fmt.Sprintf("域 %s：表 %s 不在事实包中（剔除）", d.Name, t))
			}
		}
		// R38：services 校验（服务名须在事实包 services 名单）
		for _, s := range d.Services {
			if haveSvc[s] {
				svcs = append(svcs, s)
			} else {
				warns = append(warns, fmt.Sprintf("域 %s：服务 %s 不在事实包中（剔除）", d.Name, s))
			}
		}
		if len(pkgs) == 0 && len(tbls) == 0 {
			warns = append(warns, fmt.Sprintf("域 %s：无有效归属（剔除）", d.Name))
			continue
		}
		// R80：subdomains 校验（清洗——不在事实包剔除并警告）
		var sw []string
		d.Subdomains, sw = SanitizeSubdomains(d, havePkg, haveTbl)
		warns = append(warns, sw...)
		d.Packages, d.Tables, d.Services = pkgs, tbls, svcs
		doms = append(doms, d)
	}
	return doms, warns
}

// SanitizeSubdomains 子域校验：包/表逐项校验（不在事实包剔除并警告）；
// 无有效归属的子域整体剔除。返回清洗后的子域列表 + 警告。
func SanitizeSubdomains(d WikiDomainCfg, havePkg, haveTbl map[string]bool) ([]WikiSubdomainCfg, []string) {
	var subs []WikiSubdomainCfg
	var warns []string
	for _, sd := range d.Subdomains {
		if sd.Name == "" {
			warns = append(warns, fmt.Sprintf("域 %s：跳过无名称的子域", d.Name))
			continue
		}
		var sp, st []string
		for _, p := range sd.Packages {
			if havePkg[p] {
				sp = append(sp, p)
			} else {
				warns = append(warns, fmt.Sprintf("子域 %s/%s：包 %s 不在事实包中（剔除）", d.Name, sd.Name, p))
			}
		}
		for _, t := range sd.Tables {
			if haveTbl[t] {
				st = append(st, t)
			} else {
				warns = append(warns, fmt.Sprintf("子域 %s/%s：表 %s 不在事实包中（剔除）", d.Name, sd.Name, t))
			}
		}
		if len(sp) == 0 && len(st) == 0 {
			warns = append(warns, fmt.Sprintf("子域 %s/%s：无有效归属（剔除）", d.Name, sd.Name))
			continue
		}
		sd.Packages, sd.Tables = sp, st
		subs = append(subs, sd)
	}
	return subs, warns
}

// StripYAMLFence 剥离 AI 输出的 ```yaml 围栏（缺尾围栏也容忍）。
func StripYAMLFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	lines = lines[1:]
	if n := len(lines); n > 0 && strings.HasPrefix(strings.TrimSpace(lines[n-1]), "```") {
		lines = lines[:n-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// DomainFactsJSON 事实包 JSON（compact——R61：AI 读取的文件不 format，
// 避免文件过大消耗 token；agent 用 Read 工具读，缩进无收益）。
func DomainFactsJSON(f *DomainFacts) ([]byte, error) {
	return json.Marshal(f)
}

// DomainFactsJSONIndent 事实包 JSON（缩进版——--export-facts 人工检查
// 用；AI 读取路径用 compact 版）。
func DomainFactsJSONIndent(f *DomainFacts) ([]byte, error) {
	return json.MarshalIndent(f, "", "  ")
}

// DomainPrompt 组装 AI prompt（**事实不内联**——引用已导出的 JSON
// 事实文件，agent 先读文件再分析；信息充分性靠文件完整性）。
// extraPrompt：用户约束（R56 wiki --prompt——预先指定部分域，帮助
// AI 判断；空 = 无约束）。
// R72：要求 AI 把归纳结果写入 JSON 文件（.codeintel/domains-ai.json）
// ——响应只回 done；程序读文件解析（文件是权威来源）。
func DomainPrompt(factsPath, extraPrompt string) string {
	var b strings.Builder
	b.WriteString("你是代码架构分析师。代码静态分析事实已导出到 JSON 文件 `" + factsPath + "`（packages/tables/entities/services，权威可靠）。\n")
	b.WriteString("请先用 Read 工具读取该文件，然后归纳该项目的**业务域（领域）划分**。\n\n")
	b.WriteString("要求：\n")
	b.WriteString("1. 划分 3~8 个业务域，**覆盖文件中的全部包与表**（未覆盖的会丢失归属）\n")
	b.WriteString("2. 每个域：name（中文业务名，如「商品域」）、description（一句话职责）、packages（归属包**完整路径**列表——用文件中 packages[].path）、tables（归属表名列表——用文件中 tables[].name）、services（归属服务名列表——用文件中 services[].name，grpc 服务名或 http \"METHOD path\"）\n")
	b.WriteString("3. **grpc 服务可能含多域方法**（services[].methods 方法名列表——服务定义大而分开部署）：方法明显属于其他域时，把服务名写入方法所属域（服务仍可在原域）\n")
	b.WriteString("4. 包、表、服务只归属一个域；文件里没有的包路径、表名、服务名一律不要写\n")
	b.WriteString("5. **services 字段必填**：每个域给出归属的服务名列表（grpc 服务名如 OrderService/MemberService 按业务语义归属；无法归属的服务可以不放任何域，但能归属的必须写上）\n")
	b.WriteString("6. 只输出 YAML，不要解释：\n")
	b.WriteString("domains:\n  - name: 商品域\n    description: 商品/SKU/类目管理\n    packages: [github.com/ixre/go2o/pkg/interface/domain/item]\n    tables: [item_info, item_sku]\n    services: [ItemService]\n")
	b.WriteString("\n7. **调用热度辅助**（entities 的 out/in = 调出/被调聚合边数）：相互调用密集（out/in 高且互相关联）的实体尽量归同一域——领域内聚、跨域调用边少；单域实体过多（密集调用域内边会爆炸）时优先把调用稀疏的边界实体拆到其他域\n")
	b.WriteString("\n8. **包级调用矩阵**（pkg_calls：from = 调用方包完整路径，to = [{pkg, count}] 被调目标数组——同 from 聚合、同包调用已不计）：子域划分参考包间调用密度——**调用密集的包组归同一子域（内聚），包间调用稀疏处是子域边界**；实体归属先归包（entities.pkg）再随包归子域\n")
	b.WriteString("\n9. **包规模与角色**：packages[].ents = 包内实体数（大头包——实体多的包建议拆子域或与其他包分域）；entities[].service = 行为载体（无字段/组合注入——service 按职责归域）vs 数据载体（字段被写——随所属 service 归域，不独立成域）\n")
	b.WriteString("\n10. **规模基准（渲染上限）**：每个域的内部协作图调用边超过 500 条、实体超过约 30 个时渲染失败或降级。划分时**每域实体数建议 ≤15**（按 packages[].ents 预估）——实体多的包拆到多个域或拆子域；宁可多几个域，不要单域过大\n")
	b.WriteString("\n11. **输出方式（R73）**：把归纳结果**直接输出到响应文本**（JSON 格式：{\"domains\": [{\"name\", \"description\", \"packages\": [], \"tables\": [], \"services\": [], \"subdomains\": [{\"name\", \"description\", \"packages\": [], \"tables\": []}]}]}）——**不要任何说明文字、不要 markdown 围栏**；如环境支持写文件（Write 工具）可同时写入 `.codeintel/domains-ai.json`（可选，响应仍是主交付物）\n")
	b.WriteString("\n12. **每个域必须划分子域（subdomains）**：域内按语义拆分 2~5 个子域（域本身不大也至少 1 个——默认整个域为一个子域）。子域 = 域内职责内聚的包+表分组（参考 pkg_calls 调用密度——密集包组同子域、稀疏处是子域边界）；每个子域给出 name（中文，如「订单核心」）、description（一句话）、packages（归属包完整路径）、tables（归属表名）——**域内全部包和表都要归入某个子域**；实体多的域（>15 实体或 packages[].ents 大头包）必须拆分多个子域\n")
	if extraPrompt != "" {
		b.WriteString("\n用户额外约束（**必须优先遵守**，冲突时以用户约束为准）：\n" + extraPrompt + "\n")
	}
	return b.String()
}
