package action

// R34 `domains` 事实包（批次 C 迁移，原 cli/domains_facts.go）：结构化
// JSON——静态分析全算好，AI 只做语义归纳（信息充分性靠数据完整性）。
// 事实包类型/收集/JSON 序列化迁 action；cli 只留参数解析与输出。

import (
	"fmt"
	"sort"
	"strings"
)

// PkgFacts 一个包的事实（完整路径——层级由 path 承载，归属校验用）。
type PkgFacts struct {
	Path string `json:"path"` // 完整包路径
	Doc  string `json:"doc,omitempty"`
	// Ents R70：包内实体数（实体图按包统计——包规模信号；AI 识别
	// 大头包：实体多的包必然要拆子域）
	Ents int `json:"ents,omitempty"`
}

// TableFacts 一张表的事实。
type TableFacts struct {
	Name  string `json:"name"`
	Cols  int    `json:"cols"`
	Alias string `json:"alias,omitempty"`
}

// EntityFacts 一个核心实体（类型 + 方法数 + 调用热度 + 所属包）。
// R64：Out/In = 调出/被调聚合边数——AI 划分领域参考调用热度。
// R65：Pkg = 所属包完整路径（与 packages[].path 一致——三层打通）。
// R68/R70：Service = struct 角色（方法里无字段 direct_write）。
type EntityFacts struct {
	Name    string `json:"name"`
	Pkg     string `json:"pkg"`
	Methods int    `json:"methods"`
	Out     int    `json:"out,omitempty"` // 出度：调出调用次数
	In      int    `json:"in,omitempty"`  // 入度：被调调用次数
	Service bool   `json:"service,omitempty"`
}

// PkgCallTarget 一个调用目标（被调包 + 次数）。
type PkgCallTarget struct {
	Pkg   string `json:"pkg"`   // 被调包完整路径
	Count int    `json:"count"` // 调用次数（聚合）
}

// PkgCallFacts 包级调用矩阵（R65：跨包子域划分的直接依据——子域 =
// 高内聚包组；同包调用与子域划分无关，不计）。R74：聚合数组形态。
type PkgCallFacts struct {
	From string          `json:"from"` // 调用方包完整路径
	To   []PkgCallTarget `json:"to"`   // 被调目标数组（同 from 聚合）
}

// SvcFacts 一个服务（grpc 服务名 / http 方法+路径）。
// R54：grpc 服务带方法名列表——一个服务定义可能包含多个域的方法
// （分开部署），AI 需要方法级归属信息。
type SvcFacts struct {
	Type    string   `json:"type"`              // grpc | http
	Name    string   `json:"name"`              // grpc 服务名 / http "METHOD path"
	Methods []string `json:"methods,omitempty"` // grpc 服务方法名（http 空）
}

// DomainFacts AI 分析前的结构化事实包（JSON 序列化——导出文件）。
type DomainFacts struct {
	Pkgs     []PkgFacts     `json:"packages"`
	Tables   []TableFacts   `json:"tables"`
	Ents     []EntityFacts  `json:"entities"`
	Svcs     []SvcFacts     `json:"services"`
	PkgCalls []PkgCallFacts `json:"pkg_calls,omitempty"` // R65：包级调用矩阵
}

// DomainFactsRequest 事实包收集参数（表别名来自 wiki.yaml tables——
// 配置读取在 cli，action 只收数据）。
type DomainFactsRequest struct {
	RepoAbs      string
	TableAliases map[string]string // 表别名（wiki.yaml tables[].alias）
}

// collectDomainFacts 收集事实包（复用 action 查询——包用**完整路径**；
// 注释 rune 安全截断——字节截断会切坏 UTF-8 多字节字符）。数据源失败
// 静默降级（与原 cli 行为一致——各查询独立容错）。
func (a *Actions) collectDomainFacts(req DomainFactsRequest) *DomainFacts {
	f := &DomainFacts{}

	if pkgs, err := a.Packages(); err == nil {
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
			f.Pkgs = append(f.Pkgs, PkgFacts{Path: SymbolPkg(string(p.ID)), Doc: doc})
		}
		sort.Slice(f.Pkgs, func(i, j int) bool { return f.Pkgs[i].Path < f.Pkgs[j].Path })
	}

	if cols, err := a.GetAllTableColumns(); err == nil {
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
			f.Tables = append(f.Tables, TableFacts{Name: t, Cols: byTbl[t], Alias: req.TableAliases[t]})
		}
	}

	if g, err := a.Entities(); err == nil && g != nil {
		// R64/R66：实体出度/入度（调用次数聚合 Count——真实调用热度）
		out := map[string]int{}
		in := map[string]int{}
		for _, e := range g.Edges {
			out[e.From] += e.Count
			in[e.To] += e.Count
		}
		// R66：截断前按调用热度（Out+In）降序——AI 看到的是核心实体；
		// R68/R70：Service 标记（struct 角色——行为载体 vs 数据载体）
		for _, n := range g.Nodes {
			f.Ents = append(f.Ents, EntityFacts{Name: n.Name, Pkg: n.Pkg, Methods: n.MethodCount,
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
		// R65：包级调用矩阵（跨包非零边；同包调用与子域划分无关不计）
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
		// R70：包规模统计——包内实体数（AI 识别大头包）
		pkgEnts := map[string]int{}
		for _, n := range g.Nodes {
			pkgEnts[n.Pkg]++
		}
		for i := range f.Pkgs {
			f.Pkgs[i].Ents = pkgEnts[f.Pkgs[i].Path]
		}
		// R74：同 from 聚合到 to 数组（减小 facts 体积）
		byFrom := map[string][]PkgCallTarget{}
		for pair, c := range pkgCalls {
			byFrom[pair[0]] = append(byFrom[pair[0]], PkgCallTarget{Pkg: pair[1], Count: c})
		}
		for from, targets := range byFrom {
			sort.Slice(targets, func(i, j int) bool { return targets[i].Pkg < targets[j].Pkg })
			f.PkgCalls = append(f.PkgCalls, PkgCallFacts{From: from, To: targets})
		}
		sort.Slice(f.PkgCalls, func(i, j int) bool { return f.PkgCalls[i].From < f.PkgCalls[j].From })
	}

	if res, err := a.GrpcRoutes(req.RepoAbs); err == nil {
		for _, s := range res.Services {
			// R54：服务带方法名；R71：方法名截断（前 20 + 总数——token 大头）
			svc := SvcFacts{Type: "grpc", Name: s.Name}
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
	if res, err := a.HTTPRoutes(); err == nil {
		for _, r := range res.Routes {
			m := r.Method
			if m == "" {
				m = "ANY"
			}
			f.Svcs = append(f.Svcs, SvcFacts{Type: "http", Name: m + " " + r.Path})
		}
	}
	if len(f.Svcs) > 40 {
		f.Svcs = f.Svcs[:40]
	}
	return f
}
