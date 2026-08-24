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
// 包用**完整路径**（层级树渲染 + 归属校验；短名歧义在完整路径下消除）；
// 注释 rune 安全截断（doc[:80] 字节截断会切坏 UTF-8 多字节字符——
// 用户实测导出文件出现无效 UTF-8）。
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
// 用户要求：不内联进 prompt，先导出文件再让 agent 读；**包清单按
// 路径层级树形**表示层级关系）。
func domainFactsText(f *domainFacts) string {
	var b strings.Builder
	b.WriteString("代码静态分析事实（权威可靠——包/表/实体/服务全部来自索引）：\n\n")
	b.WriteString("一、包清单（路径层级树——叶节点为包，括号内是完整路径）：\n")
	for _, line := range pkgTreeLines(f.Pkgs) {
		b.WriteString(line + "\n")
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

// pkgTreeLines 包路径层级树（用户要求：包列表层级关系表示）。目录行
// `- seg/` 缩进表示层级，叶节点 `- 包名 | 注释（完整路径:…）`。
func pkgTreeLines(pkgs []pkgFacts) []string {
	type node struct {
		name     string
		children map[string]*node
		leaf     *pkgFacts
	}
	root := &node{children: map[string]*node{}}
	for i := range pkgs {
		parts := strings.Split(pkgs[i].Path, "/")
		cur := root
		for _, seg := range parts[:len(parts)-1] {
			if cur.children[seg] == nil {
				cur.children[seg] = &node{name: seg, children: map[string]*node{}}
			}
			cur = cur.children[seg]
		}
		leafName := parts[len(parts)-1]
		if cur.children[leafName] == nil {
			cur.children[leafName] = &node{name: leafName, children: map[string]*node{}}
		}
		cur.children[leafName].leaf = &pkgs[i]
	}
	var out []string
	var walk func(n *node, depth int)
	walk = func(n *node, depth int) {
		indent := strings.Repeat("  ", depth)
		if n.leaf != nil {
			line := "- " + n.name
			if n.leaf.Doc != "" {
				line += " | " + n.leaf.Doc
			}
			line += "（" + n.leaf.Path + "）"
			out = append(out, indent+line)
			return
		}
		if n.name != "" {
			out = append(out, indent+"- "+n.name+"/")
		}
		names := make([]string, 0, len(n.children))
		for k := range n.children {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			walk(n.children[k], depth+1)
		}
	}
	walk(root, 0)
	return out
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
	b.WriteString("2. 每个域：name（中文业务名，如「商品域」）、description（一句话职责）、packages（归属包**完整路径**列表——用文件包树叶子括号里的完整路径）、tables（归属表名列表——用文件表清单中的名字）\n")
	b.WriteString("3. 包与表只归属一个域；文件里没有的包完整路径、表名一律不要写\n")
	b.WriteString("4. 只输出 YAML，不要解释：\n")
	b.WriteString("domains:\n  - name: 商品域\n    description: 商品/SKU/类目管理\n    packages: [github.com/ixre/go2o/pkg/interface/domain/item]\n    tables: [item_info, item_sku]\n")
	return b.String()
}
