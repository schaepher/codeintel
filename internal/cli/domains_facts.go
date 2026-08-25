package cli

// R34 domains 事实包：结构化 JSON（用户要求——导出文件用 JSON 格式，
// agent 读 JSON 文件；层级关系由完整路径天然承载）。静态分析全算好，
// AI 只做语义归纳——信息充分性靠数据完整性。

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// pkgFacts 一个包的事实（完整路径——层级由 path 承载，归属校验用；
// 短名歧义（go2o 多个 member 包）在完整路径下消除）。
type pkgFacts struct {
	Path string `json:"path"` // 完整包路径
	Doc  string `json:"doc,omitempty"`
}

// tableFacts 一张表的事实。
type tableFacts struct {
	Name  string `json:"name"`
	Cols  int    `json:"cols"`
	Alias string `json:"alias,omitempty"`
}

// entityFacts 一个核心实体（类型 + 方法数）。
type entityFacts struct {
	Name    string `json:"name"`
	Methods int    `json:"methods"`
}

// svcFacts 一个服务（grpc 服务名 / http 方法+路径）。
// R54：grpc 服务带方法名列表——一个服务定义可能包含多个域的方法
// （分开部署），AI 需要方法级归属信息。
type svcFacts struct {
	Type    string   `json:"type"`    // grpc | http
	Name    string   `json:"name"`    // grpc 服务名 / http "METHOD path"
	Methods []string `json:"methods,omitempty"` // grpc 服务方法名（http 空）
}

// domainFacts AI 分析前的结构化事实包（JSON 序列化——导出文件）。
type domainFacts struct {
	Pkgs   []pkgFacts    `json:"packages"`
	Tables []tableFacts  `json:"tables"`
	Ents   []entityFacts `json:"entities"`
	Svcs   []svcFacts    `json:"services"`
}

// collectDomainFacts 收集事实包（复用索引查询——动作层 API）。
// 包用**完整路径**；注释 rune 安全截断（字节截断会切坏 UTF-8 多字节
// 字符——用户实测导出文件出现无效 UTF-8）。
func collectDomainFacts(acts *action.Actions, repoAbs string, cfg wikiConfig, db *sqlite.Repo) *domainFacts {
	f := &domainFacts{}

	if pkgs, err := acts.Packages(); err == nil {
		for _, p := range pkgs {
			doc := ""
			if d := p.DocComment(); d != "" {
				doc = strings.TrimSpace(strings.SplitN(d, "\n", 2)[0])
				// 块注释 `/* ... */` 首行常带 `*`——剥掉
				doc = strings.TrimPrefix(doc, "*")
				doc = strings.TrimSpace(doc)
				if doc == "" || doc == "*" {
					doc = ""
				} else if r := []rune(doc); len(r) > 60 {
					doc = string(r[:60])
				}
			}
			f.Pkgs = append(f.Pkgs, pkgFacts{Path: symbolPkg(string(p.ID)), Doc: doc})
		}
		sort.Slice(f.Pkgs, func(i, j int) bool { return f.Pkgs[i].Path < f.Pkgs[j].Path })
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
			f.Tables = append(f.Tables, tableFacts{Name: t, Cols: byTbl[t], Alias: aliasOf[t]})
		}
	}

	if g, err := acts.Entities(); err == nil && g != nil {
		for _, n := range g.Nodes {
			if len(f.Ents) >= 60 {
				break
			}
			f.Ents = append(f.Ents, entityFacts{Name: n.Name, Methods: n.MethodCount})
		}
	}

	if res, err := grpcRoutes(db, repoAbs); err == nil {
		for _, s := range res.Services {
			// R54：服务带方法名（一个服务可能含多域方法——分开部署）
			svc := svcFacts{Type: "grpc", Name: s.Name}
			for _, m := range s.Methods {
				svc.Methods = append(svc.Methods, m.Name)
			}
			f.Svcs = append(f.Svcs, svc)
		}
	}
	if res, err := httpRoutes(db); err == nil {
		for _, r := range res.Routes {
			m := r.Method
			if m == "" {
				m = "ANY"
			}
			f.Svcs = append(f.Svcs, svcFacts{Type: "http", Name: m + " " + r.Path})
		}
	}
	if len(f.Svcs) > 40 {
		f.Svcs = f.Svcs[:40]
	}
	return f
}

// domainFactsJSON 事实包 JSON（导出文件内容——用户要求 JSON 格式，
// agent 读 JSON；indent 2 可读）。
func domainFactsJSON(f *domainFacts) ([]byte, error) {
	return json.MarshalIndent(f, "", "  ")
}

// exportDomainFacts 事实包导出到文件（--export-facts——JSON 格式，
// 可人工检查/喂给任何 agent）。
func exportDomainFacts(repoAbs string, acts *action.Actions, cfg wikiConfig, db *sqlite.Repo, path string) error {
	f := collectDomainFacts(acts, repoAbs, cfg, db)
	b, err := domainFactsJSON(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// domainPrompt 组装 AI prompt（**事实不内联**——引用已导出的 JSON
// 事实文件，agent 先读文件再分析；信息充分性靠文件完整性）。
func domainPrompt(factsPath string) string {
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
	return b.String()
}
