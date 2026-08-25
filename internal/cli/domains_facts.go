package cli

// R34 domains 事实包：结构化 JSON（用户要求——导出文件用 JSON 格式，
// agent 读 JSON 文件；层级关系由完整路径天然承载）。静态分析全算好，
// AI 只做语义归纳——信息充分性靠数据完整性。

import (
	"encoding/json"
	"fmt"
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
	// Ents R70：包内实体数（实体图按包统计——包规模信号；AI 识别
	// 大头包：实体多的包必然要拆子域）
	Ents int `json:"ents,omitempty"`
}

// tableFacts 一张表的事实。
type tableFacts struct {
	Name  string `json:"name"`
	Cols  int    `json:"cols"`
	Alias string `json:"alias,omitempty"`
}

// entityFacts 一个核心实体（类型 + 方法数 + 调用热度 + 所属包）。
// R64：Out/In = 调出/被调聚合边数——AI 划分领域参考调用热度（高内聚
// 实体同域、跨域调用边少；实体多的域提示可再细分——防域内边爆炸）。
// R65：Pkg = 所属包完整路径（与 packages[].path 一致——AI 归属实体
// → 包 → 子域，三层打通）。
// R68/R70：Service = struct 角色（方法里无字段 direct_write——无字段
// 结构体/组合注入/client 字段 → service 行为载体；字段被写 → 数据
// 载体）——AI 划分时区分行为载体与数据载体。
type entityFacts struct {
	Name    string `json:"name"`
	Pkg     string `json:"pkg"`
	Methods int    `json:"methods"`
	Out     int    `json:"out,omitempty"` // 出度：调出调用次数
	In      int    `json:"in,omitempty"`  // 入度：被调调用次数
	Service bool   `json:"service,omitempty"`
}

// pkgCallFacts 包级调用矩阵（R65：跨包子域划分的直接依据——子域 =
// 高内聚包组；同包调用与子域划分无关，不计）。
type pkgCallFacts struct {
	From  string `json:"from"`  // 调用方包完整路径
	To    string `json:"to"`    // 被调包完整路径
	Count int    `json:"count"` // 调用次数（聚合）
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
	Pkgs     []pkgFacts      `json:"packages"`
	Tables   []tableFacts    `json:"tables"`
	Ents     []entityFacts   `json:"entities"`
	Svcs     []svcFacts      `json:"services"`
	PkgCalls []pkgCallFacts  `json:"pkg_calls,omitempty"` // R65：包级调用矩阵
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
		// R64/R66：实体出度/入度（调用次数聚合 Count——真实调用热度，
		// 非边条数；AI 领域划分参考）
		out := map[string]int{}
		in := map[string]int{}
		for _, e := range g.Edges {
			out[e.From] += e.Count
			in[e.To] += e.Count
		}
		// R65：实体带包路径（与 packages[].path 一致——实体→包→子域）；
		// R66：截断前按调用热度（Out+In）降序——AI 看到的是核心实体
		// （此前按字母序截断——前 60 个是 a/aftersales 开头，偏差）；
		// R68/R70：Service 标记（struct 角色——行为载体 vs 数据载体）
		for _, n := range g.Nodes {
			f.Ents = append(f.Ents, entityFacts{Name: n.Name, Pkg: n.Pkg, Methods: n.MethodCount,
				Out: out[n.ID], In: in[n.ID], Service: n.Service})
		}
		sort.Slice(f.Ents, func(i, j int) bool {
			hi := f.Ents[i].Out + f.Ents[i].In
			hj := f.Ents[j].Out + f.Ents[j].In
			if hi != hj {
				return hi > hj
			}
			if f.Ents[i].Pkg != f.Ents[j].Pkg {
				return f.Ents[i].Pkg < f.Ents[j].Pkg
			}
			return f.Ents[i].Name < f.Ents[j].Name
		})
		if len(f.Ents) > 60 {
			f.Ents = f.Ents[:60]
		}
		// R65：包级调用矩阵（跨包非零边——子域划分的直接依据；
		// 同包调用与子域划分无关不计）
		pkgOfID := map[string]string{}
		for _, n := range g.Nodes {
			pkgOfID[n.ID] = n.Pkg
		}
		pkgCalls := map[[2]string]int{}
		for _, e := range g.Edges {
			fp, ok1 := pkgOfID[e.From]
			tp, ok2 := pkgOfID[e.To]
			if !ok1 || !ok2 || fp == tp {
				continue
			}
			pkgCalls[[2]string{fp, tp}] += e.Count
		}
		// R70：包规模统计——包内实体数（实体图按包；AI 识别大头包）
		pkgEnts := map[string]int{}
		for _, n := range g.Nodes {
			pkgEnts[n.Pkg]++
		}
		for i := range f.Pkgs {
			f.Pkgs[i].Ents = pkgEnts[f.Pkgs[i].Path]
		}
		for pair, c := range pkgCalls {
			f.PkgCalls = append(f.PkgCalls, pkgCallFacts{From: pair[0], To: pair[1], Count: c})
		}
		sort.Slice(f.PkgCalls, func(i, j int) bool {
			if f.PkgCalls[i].From != f.PkgCalls[j].From {
				return f.PkgCalls[i].From < f.PkgCalls[j].From
			}
			return f.PkgCalls[i].To < f.PkgCalls[j].To
		})
	}

	if res, err := grpcRoutes(db, repoAbs); err == nil {
		for _, s := range res.Services {
			// R54：服务带方法名（一个服务可能含多域方法——分开部署）；
			// R71：方法名截断（前 20 + 总数——MemberService 100+ 方法
			// 全量是 token 大头；AI 多域归属判断不需要全部方法名）
			svc := svcFacts{Type: "grpc", Name: s.Name}
			names := make([]string, 0, len(s.Methods))
			for _, m := range s.Methods {
				names = append(names, m.Name)
			}
			const maxMethods = 20
			if len(names) > maxMethods {
				names = append(names[:maxMethods], fmt.Sprintf("…共 %d 个", len(names)))
			}
			svc.Methods = names
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

// domainFactsJSON 事实包 JSON（compact——R61：AI 读取的文件不 format，
// 避免文件过大消耗 token；agent 用 Read 工具读，缩进无收益）。
func domainFactsJSON(f *domainFacts) ([]byte, error) {
	return json.Marshal(f)
}

// domainFactsJSONIndent 事实包 JSON（缩进版——--export-facts 人工检查
// 用；AI 读取路径用 compact 版）。
func domainFactsJSONIndent(f *domainFacts) ([]byte, error) {
	return json.MarshalIndent(f, "", "  ")
}

// exportDomainFacts 事实包导出到文件（--export-facts——JSON 格式，
// 可人工检查/喂给任何 agent；缩进版可读）。
func exportDomainFacts(repoAbs string, acts *action.Actions, cfg wikiConfig, db *sqlite.Repo, path string) error {
	f := collectDomainFacts(acts, repoAbs, cfg, db)
	b, err := domainFactsJSONIndent(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// domainPrompt 组装 AI prompt（**事实不内联**——引用已导出的 JSON
// 事实文件，agent 先读文件再分析；信息充分性靠文件完整性）。
// extraPrompt：用户约束（R56 wiki --prompt——预先指定部分域，帮助
// AI 判断；空 = 无约束）。
func domainPrompt(factsPath, extraPrompt string) string {
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
	b.WriteString("\n8. **包级调用矩阵**（pkg_calls：from/to = 包完整路径、count = 调用次数——同包调用已不计）：子域划分参考包间调用密度——**调用密集的包组归同一子域（内聚），包间调用稀疏处是子域边界**；实体归属先归包（entities.pkg）再随包归子域\n")
	b.WriteString("\n9. **包规模与角色**：packages[].ents = 包内实体数（大头包——实体多的包建议拆子域或与其他包分域）；entities[].service = 行为载体（无字段/组合注入——service 按职责归域）vs 数据载体（字段被写——随所属 service 归域，不独立成域）\n")
	b.WriteString("\n10. **规模基准（渲染上限）**：每个域的内部协作图调用边超过 500 条、实体超过约 30 个时渲染失败或降级。划分时**每域实体数建议 ≤15**（按 packages[].ents 预估）——实体多的包拆到多个域或拆子域；宁可多几个域，不要单域过大\n")
	if extraPrompt != "" {
		b.WriteString("\n用户额外约束（**必须优先遵守**，冲突时以用户约束为准）：\n" + extraPrompt + "\n")
	}
	return b.String()
}
