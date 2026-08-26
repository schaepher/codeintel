package cli

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// pkgCodeFactsFor 包内代码事实（fallback——无包级说明时展示结构体/
// 方法/函数签名，用户要求）。R9x：SQL 收口到仓储层
// （Repo.GetPkgCodeFacts）。
func pkgCodeFactsFor(repo *sqlite.Repo, pkgPath string) *domain.PkgCodeFacts {
	if repo == nil {
		return nil
	}
	facts, err := repo.GetPkgCodeFacts(pkgPath)
	if err != nil {
		return nil
	}
	return facts
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
		// R93：完整包名（同末尾名无法区分）
		name := p.Name
		id := fmt.Sprintf("pkg-%d", i)
		// R82：每个包 fold-btn 折叠（默认折叠）
		b.WriteString(fmt.Sprintf(`<h4 class="fold-btn" data-target="%s" data-label="1">▸ <code>%s</code></h4><div class="sec-body" id="%s" style="display:none">`,
			id, htmlEsc(name), id))
		doc := action.PackageDoc(p)
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
		// R93：完整包名（同末尾名无法区分）
		name := p.Name
		b.WriteString("<details><summary><code>" + name + "</code></summary>\n\n")
		doc := action.PackageDoc(p)
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
