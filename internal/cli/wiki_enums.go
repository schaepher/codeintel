package cli

// R5 枚举与工具函数清单：项目关键信息（常量值/helper）进 wiki +
// query enums --json + MCP 工具——AI Agent 直接获取权威枚举值，
// 避免重复定义导致值不一致（如边类型 calls 被写成 call）。
// 数据源：源码 go/ast 提取类型化/字符串 const（代码事实）。

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// enumEntry 一个枚举常量。
type enumEntry struct {
	Pkg     string `json:"pkg"`     // 包路径（短名）/ proto package
	Type    string `json:"type"`    // 枚举类型（空 = 无类型 const 组）
	Group   string `json:"group"`   // 所在 const 块（首常量名）/ 枚举名
	Name    string `json:"name"`    // 常量名/值名
	Value   string `json:"value"`   // 字符串值/值号
	Comment string `json:"comment"` // 行内注释
	File    string `json:"file"`    // 定义文件
	Line    int    `json:"line"`    // 定义行
	Source  string `json:"source"`  // 来源：go | proto（R29 grpc 枚举）
}

// extractEnums 提取仓库内字符串枚举常量（类型化或 const 块内字符串
// 字面量）——排除 _test.go 与外部目录（R29：全仓扫，不再限 internal/；
// 跳过 _pb.go 生成代码——枚举由 .proto 源提供）。onlyTyped=true 时
// 只返回有显式类型的枚举（无类型字符串常量多为展示标签——默认过滤；
// --include-untyped 放开）。R29：并入 .proto 源枚举（Source=proto）。
func extractEnums(repoAbs string, onlyTyped bool) []enumEntry {
	var out []enumEntry
	root := repoAbs
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".pb.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}
		pkg := f.Name.Name
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			var groupName string // const 块首常量名（分组）
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) == 0 || len(vs.Values) == 0 {
					continue
				}
				// 值须为字符串字面量（枚举特征）
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconvUnquote(lit.Value)
				if err != nil {
					continue
				}
				// R5：长文本常量（usageText/wikiGuide 等文档模板）非枚举
				if len(val) > 64 {
					continue
				}
				typ := ""
				if vs.Type != nil {
					typ = exprName(vs.Type)
				}
				// R6：只返回有类型枚举（默认）；无类型常量（展示标签等）
				// 仅在 includeUntyped 时返回
				if onlyTyped && typ == "" {
					continue
				}
				if groupName == "" {
					groupName = vs.Names[0].Name
				}
				comment := ""
				if vs.Comment != nil {
					comment = strings.TrimSpace(strings.TrimPrefix(vs.Comment.Text(), "//"))
				}
				out = append(out, enumEntry{
					Pkg: pkg, Type: typ, Group: groupName,
					Name: vs.Names[0].Name, Value: val, Comment: comment,
					File: filepath.ToSlash(path), Line: fset.Position(vs.Pos()).Line,
					Source: "go",
				})
			}
		}
		return nil
	})
	// R29：并入 .proto 源枚举（grpc 枚举权威值——proto 定义）
	out = append(out, extractProtoEnums(repoAbs)...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pkg != out[j].Pkg {
			return out[i].Pkg < out[j].Pkg
		}
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// exprName AST 表达式短名（*ast.Ident / SelectorExpr 末段）。
func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// strconvUnquote 去掉字符串字面量引号（含反引号）。
func strconvUnquote(s string) (string, error) {
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1], nil
	}
	return strconv.Unquote(s)
}

// cmdEnums 实现 `codeintel query enums [--repo <path>] [--json]`——
// 源码枚举权威清单（不依赖索引；AI Agent 直接获取避免重定义）。
func cmdEnums(repoAbs string, f queryFlags) int {
	entries := extractEnums(repoAbs, !f.includeUntyped)
	if f.json {
		b, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	// 文本：分组展示（类型 → 名称 = 值 注释）
	group := ""
	for _, e := range entries {
		key := e.Type
		if key == "" {
			key = e.Group
		}
		if key != group {
			group = key
			fmt.Printf("\n[%s]（%s 包）\n", key, e.Pkg)
		}
		fmt.Printf("  %-28s = %-24q %s\n", e.Name, e.Value, e.Comment)
	}
	fmt.Printf("\n共 %d 个枚举常量\n", len(entries))
	return 0
}

// renderEnumsMD 枚举页 Markdown（含索引实际值分布对照 + 工具函数）。
// R88：工具函数 = 游离函数且被 ≥3 个包调用（helpers.min_packages
// 可调）——与 `query helpers` 同源（queryHelpers）。
func renderEnumsMD(repoAbs string, repo *sqlite.Repo) string {
	entries := extractEnums(repoAbs, true)
	var b strings.Builder
	b.WriteString("# 枚举与工具函数\n\n> 数据源：源码 go/ast 提取（代码事实，默认只显示有类型枚举）\n")
	b.WriteString("> ——AI/Agent 直接引用这些权威值，勿重新定义（值不一致会导致\n")
	b.WriteString("> 转换成本）；`query enums --include-untyped` 可含无类型常量。\n\n")
	group := ""
	for _, e := range entries {
		key := e.Type
		if key == "" {
			key = e.Group
		}
		if key != group {
			group = key
			b.WriteString("## " + key + "\n\n")
			b.WriteString("| 名称 | 值 | 说明 | 位置 |\n|---|---|---|---|\n")
		}
		loc := fmt.Sprintf("%s:%d", e.File, e.Line)
		b.WriteString(fmt.Sprintf("| %s | `%s` | %s | %s |\n", e.Name, e.Value, e.Comment, loc))
	}
	if len(entries) == 0 {
		b.WriteString("（未提取到枚举常量）\n")
	}
	return b.String()
}

// renderEnumsHTML 枚举页 html 内容。
func renderEnumsHTML(repoAbs string, repo *sqlite.Repo) string {
	entries := extractEnums(repoAbs, true)
	var b strings.Builder
	b.WriteString(`<section id="enums"><h2>枚举与工具函数</h2><p class="muted">数据源：源码 go/ast 提取（有类型枚举）——权威值，勿重新定义。</p>`)
	if sec := renderHelpersHTML(repo); sec != "" {
		b.WriteString(sec)
	}
	// R87：每组枚举默认折叠（组名按钮——点击展开该组表格，减少长页
	// 面滚动；枚举区只显示组名列表）
	group := ""
	groupIdx := 0
	for _, e := range entries {
		key := e.Type
		if key == "" {
			key = e.Group
		}
		if key != group {
			if group != "" {
				b.WriteString("</table></div>")
			}
			group = key
			b.WriteString(fmt.Sprintf(`<div class="fold-btn" data-target="enum-%d" data-label="1">▸ %s</div>`,
				groupIdx, htmlEsc(key)))
			b.WriteString(fmt.Sprintf(`<div class="sec-body" id="enum-%d" style="display:none"><h3>%s</h3><table><tr><th>名称</th><th>值</th><th>说明</th><th>位置</th></tr>`,
				groupIdx, htmlEsc(key)))
			groupIdx++
		}
		b.WriteString(fmt.Sprintf("<tr><td>%s</td><td><code>%s</code></td><td>%s</td><td class=\"muted\">%s:%d</td></tr>",
			htmlEsc(e.Name), htmlEsc(e.Value), htmlEsc(e.Comment), htmlEsc(filepath.Base(e.File)), e.Line))
	}
	if group != "" {
		b.WriteString("</table></div>")
	}
	b.WriteString("</section>")
	return b.String()
}

// renderHelpersMD 工具函数 Markdown 小节（R88：游离函数 + 跨包使用
// 数 ≥ minPkgs——query helpers 同源）。
func renderHelpersMD(repo *sqlite.Repo) string {
	if repo == nil {
		return ""
	}
	minPkgs := helperMinPackages()
	helpers := queryHelpers(repo, minPkgs)
	if len(helpers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 工具函数\n\n> 游离函数且被 ≥")
	b.WriteString(fmt.Sprint(minPkgs))
	b.WriteString(" 个包调用（`query helpers` 同源；config.yaml helpers.min_packages 可调）\n\n")
	b.WriteString("| 函数 | 包数 | 调用方 |\n|---|---|---|\n")
	for _, h := range helpers {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", h.Name, h.Pkgs, h.Callers)
	}
	return b.String()
}

// renderHelpersHTML 工具函数 html 小节（同源 queryHelpers）。
func renderHelpersHTML(repo *sqlite.Repo) string {
	if repo == nil {
		return ""
	}
	minPkgs := helperMinPackages()
	helpers := queryHelpers(repo, minPkgs)
	if len(helpers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<h3 class="fold-btn" data-target="helpers" data-label="1">▸ 工具函数（游离函数，被 ≥%d 个包调用）</h3><div class="sec-body" id="helpers" style="display:none"><p class="muted">`+`query helpers`+` 同源；config.yaml helpers.min_packages 可调</p><table><tr><th>函数</th><th>包数</th><th>调用方</th></tr>`, minPkgs))
	for _, h := range helpers {
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td>%d</td><td>%d</td></tr>", htmlEsc(h.Name), h.Pkgs, h.Callers)
	}
	b.WriteString("</table></div>")
	return b.String()
}
