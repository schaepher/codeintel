package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// collectDomainFacts 收集事实包（复用索引查询——动作层 API）。
func collectDomainFacts(acts *action.Actions, repoAbs string, cfg wikiConfig, db *sqlite.Repo) *domainFacts {
	f := &domainFacts{}

	if pkgs, err := acts.Packages(); err == nil {
		for _, p := range pkgs {
			name := shortSymbolNameID(string(p.ID))
			doc := ""
			if d := p.DocComment(); d != "" && d != "*" {
				doc = strings.SplitN(d, "\n", 2)[0]
				if len(doc) > 80 {
					doc = doc[:80]
				}
				doc = " | " + doc
			}
			f.Pkgs = append(f.Pkgs, name+doc)
		}
	}

	aliasOf := map[string]string{}
	for _, t := range cfg.Tables {
		aliasOf[t.Name] = t.Alias
	}
	if cols, err := acts.GetAllTableColumns(); err == nil {
		byTbl := map[string]int{}
		for _, c := range cols {
			if i := strings.Index(c.Name, "."); i > 0 {
				byTbl[c.Name[:i]]++
			}
		}
		names := make([]string, 0, len(byTbl))
		for t := range byTbl {
			names = append(names, t)
		}
		sort.Strings(names)
		for _, t := range names {
			line := fmt.Sprintf("%s（%d 列）", t, byTbl[t])
			if a := aliasOf[t]; a != "" {
				line += " 别名:" + a
			}
			f.Tables = append(f.Tables, line)
		}
	}

	if g, err := acts.Entities(); err == nil && g != nil {
		for _, n := range g.Nodes {
			if len(f.Ents) >= 60 {
				break
			}
			f.Ents = append(f.Ents, fmt.Sprintf("%s（方法%d）", n.Name, n.MethodCount))
		}
	}

	if res, err := grpcRoutes(db, repoAbs); err == nil {
		for _, s := range res.Services {
			f.Svcs = append(f.Svcs, "grpc:"+s.Name)
		}
	}
	if res, err := httpRoutes(db); err == nil {
		for _, r := range res.Routes {
			m := r.Method
			if m == "" {
				m = "ANY"
			}
			f.Svcs = append(f.Svcs, "http:"+m+" "+r.Path)
		}
	}
	if len(f.Svcs) > 40 {
		f.Svcs = f.Svcs[:40]
	}
	return f
}

// domainFactsText 事实包文本（导出文件内容/给 AI 读的权威事实——
// 用户要求：不内联进 prompt，先导出文件再让 agent 读）。
func domainFactsText(f *domainFacts) string {
	var b strings.Builder
	b.WriteString("代码静态分析事实（权威可靠——包/表/实体/服务全部来自索引）：\n\n")
	b.WriteString("一、包清单（包名 | 注释）：\n")
	for _, p := range f.Pkgs {
		b.WriteString("- " + p + "\n")
	}
	b.WriteString("\n二、数据表（表名（列数） 别名）：\n")
	for _, t := range f.Tables {
		b.WriteString("- " + t + "\n")
	}
	if len(f.Ents) > 0 {
		b.WriteString("\n三、核心实体（类型（方法数））：\n")
		for _, e := range f.Ents {
			b.WriteString("- " + e + "\n")
		}
	}
	if len(f.Svcs) > 0 {
		b.WriteString("\n四、服务（grpc/http）：\n")
		for _, s := range f.Svcs {
			b.WriteString("- " + s + "\n")
		}
	}
	return b.String()
}

// exportDomainFacts 事实包导出到文件（--export-facts——用户要求：
// 数据先落盘，可人工检查/喂给任何 agent）。
func exportDomainFacts(repoAbs string, acts *action.Actions, cfg wikiConfig, db *sqlite.Repo, path string) error {
	f := collectDomainFacts(acts, repoAbs, cfg, db)
	return os.WriteFile(path, []byte(domainFactsText(f)), 0o644)
}

// domainPrompt 组装 AI prompt（**事实不内联**——引用已导出的事实文件，
// agent 先读文件再分析；信息充分性靠文件完整性，prompt 只含指令）。
func domainPrompt(factsPath string) string {
	var b strings.Builder
	b.WriteString("你是代码架构分析师。代码静态分析事实已导出到文件 `" + factsPath + "`（包/表/实体/服务清单，权威可靠）。\n")
	b.WriteString("请先用 Read 工具读取该文件，然后归纳该项目的**业务域（领域）划分**。\n\n")
	b.WriteString("要求：\n")
	b.WriteString("1. 划分 3~8 个业务域，**覆盖文件中的全部包与表**（未覆盖的会丢失归属）\n")
	b.WriteString("2. 每个域：name（中文业务名，如「商品域」）、description（一句话职责）、packages（归属包名列表——用文件包清单中的名字）、tables（归属表名列表——用文件表清单中的名字）\n")
	b.WriteString("3. 包与表只归属一个域；文件里没有的包名、表名一律不要写\n")
	b.WriteString("4. 只输出 YAML，不要解释：\n")
	b.WriteString("domains:\n  - name: 商品域\n    description: 商品/SKU/类目管理\n    packages: [pkg_item, pkg_sku]\n    tables: [item_info, item_sku]\n")
	return b.String()
}
