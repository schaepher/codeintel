package cli

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// packageDoc 包 doc_comment 清理：去除 Copyright 行（用户要求）——
// `// Copyright ...` / `Copyright (c) ...` 等行跳过；返回清理后文本。
func packageDoc(p *domain.CodeEntity) string {
	dc, ok := p.Properties["doc_comment"].(string)
	if !ok || dc == "" {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(dc, "\n") {
		t := strings.TrimSpace(l)

		t = strings.TrimPrefix(strings.TrimPrefix(t, "//"), "*")
		t = strings.TrimSpace(t)
		if strings.HasPrefix(strings.ToLower(t), "copyright") {
			continue
		}
		lines = append(lines, l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// pkgCodeFacts 包内代码事实（fallback——无包级说明时展示结构体/方法/
// 函数签名，用户要求）。SQL 按符号 ID 前缀取包下 struct/function/method。
type pkgCodeFacts struct {
	Structs []string // 结构体名（字段数）
	Methods []string // 方法签名
	Funcs   []string // 函数签名
}

func pkgCodeFactsFor(repo *sqlite.Repo, pkgPath string) *pkgCodeFacts {
	if repo == nil {
		return nil
	}
	rows, err := repo.Query(`SELECT name, kind, COALESCE(json_extract(properties, '$.signature'), ''), COALESCE(json_extract(properties, '$.fields'), '')
		FROM nodes WHERE id LIKE ? AND kind IN ('struct','function','method')
		ORDER BY kind, name LIMIT 80`, "symbol:go:"+pkgPath+":%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := &pkgCodeFacts{}
	for rows.Next() {
		var name, kind, sig, fields string
		if err := rows.Scan(&name, &kind, &sig, &fields); err != nil {
			continue
		}
		switch kind {
		case "struct":
			n := strings.Count(fields, `"name"`)
			if n > 0 {
				out.Structs = append(out.Structs, fmt.Sprintf("%s（字段 %d）", name, n))
			} else {
				out.Structs = append(out.Structs, name)
			}
		case "method":
			if sig != "" {
				out.Methods = append(out.Methods, name+sigShort(sig))
			} else {
				out.Methods = append(out.Methods, name)
			}
		case "function":
			if sig != "" {
				out.Funcs = append(out.Funcs, name+sigShort(sig))
			} else {
				out.Funcs = append(out.Funcs, name)
			}
		}
	}

	if len(out.Structs) > 12 {
		out.Structs = out.Structs[:12]
	}
	if len(out.Methods) > 20 {
		out.Methods = out.Methods[:20]
	}
	if len(out.Funcs) > 10 {
		out.Funcs = out.Funcs[:10]
	}
	return out
}

// sigShort 签名截断（首行 + 40 runes——长签名压行）。
func sigShort(sig string) string {
	if i := strings.IndexByte(sig, '\n'); i >= 0 {
		sig = sig[:i]
	}
	if r := []rune(sig); len(r) > 60 {
		sig = string(r[:60]) + "…"
	}
	return sig
}

// renderPackagesHTML 包结构 html 内容（R1：KindPackage doc_comment；
// R34：去 Copyright；无包级说明 → fallback 包内结构体/方法/函数签名）。
func renderPackagesHTML(pkgs []*domain.CodeEntity, repo *sqlite.Repo) string {
	if len(pkgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="packages"><h2>包结构</h2><p class="muted">数据源：包节点 doc_comment（代码事实；无说明时展示包内结构体/方法/函数签名）。</p>`)
	for i, p := range pkgs {
		name := p.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		id := fmt.Sprintf("pkg-%d", i)
		// R82：每个包 fold-btn 折叠（默认折叠）
		b.WriteString(fmt.Sprintf(`<h4 class="fold-btn" data-target="%s" data-label="1">▸ <code>%s</code></h4><div class="sec-body" id="%s" style="display:none">`,
			id, htmlEsc(name), id))
		doc := packageDoc(p)
		if doc != "" {
			b.WriteString("<p>" + htmlEsc(doc) + "</p>")
			b.WriteString("</div>")
			continue
		}
		if facts := pkgCodeFactsFor(repo, symbolPkg(string(p.ID))); facts != nil {
			b.WriteString(`<p class="muted">（无包级说明——代码事实）</p>`)
			if len(facts.Structs) > 0 {
				b.WriteString("<p><strong>结构体</strong>：" + htmlEsc(strings.Join(facts.Structs, "、")) + "</p>")
			}
			// R62：方法/函数列表格
			if len(facts.Methods) > 0 {
				b.WriteString("<p><strong>方法</strong></p><table><thead><tr><th>方法</th></tr></thead><tbody>")
				for _, m := range facts.Methods {
					b.WriteString("<tr><td><code>" + htmlEsc(m) + "</code></td></tr>")
				}
				b.WriteString("</tbody></table>")
			}
			if len(facts.Funcs) > 0 {
				b.WriteString("<p><strong>函数</strong></p><table><thead><tr><th>函数</th></tr></thead><tbody>")
				for _, f := range facts.Funcs {
					b.WriteString("<tr><td><code>" + htmlEsc(f) + "</code></td></tr>")
				}
				b.WriteString("</tbody></table>")
			}
		}
		b.WriteString("</div>")
	}
	b.WriteString("</section>")
	return b.String()
}

// renderPackagesMD 包结构（R1：KindPackage 节点 doc_comment；R34：去
// Copyright；无包级说明 → fallback 代码事实）。R82：每个包 details
// 折叠（默认折叠——包多时页面长）。
func renderPackagesMD(pkgs []*domain.CodeEntity, repo *sqlite.Repo) string {
	if len(pkgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 包结构\n\n> 数据源：包节点 doc_comment（代码事实；无说明时展示包内结构体/方法/函数签名）。每个包可展开。\n\n")
	for _, p := range pkgs {
		name := p.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		b.WriteString("<details><summary><code>" + name + "</code></summary>\n\n")
		doc := packageDoc(p)
		if doc != "" {
			b.WriteString(doc + "\n\n")
			b.WriteString("</details>\n\n")
			continue
		}
		if facts := pkgCodeFactsFor(repo, symbolPkg(string(p.ID))); facts != nil {
			b.WriteString("（无包级说明——代码事实）\n\n")
			if len(facts.Structs) > 0 {
				b.WriteString("**结构体**：" + strings.Join(facts.Structs, "、") + "\n\n")
			}
			// R62：方法/函数列表格（用户要求——长清单可读性）
			if len(facts.Methods) > 0 {
				b.WriteString("**方法**\n\n")
				b.WriteString("| 方法 |\n|---|\n")
				for _, m := range facts.Methods {
					b.WriteString("| `" + m + "` |\n")
				}
				b.WriteString("\n")
			}
			if len(facts.Funcs) > 0 {
				b.WriteString("**函数**\n\n")
				b.WriteString("| 函数 |\n|---|\n")
				for _, f := range facts.Funcs {
					b.WriteString("| `" + f + "` |\n")
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("</details>\n\n")
	}
	return b.String()
}
