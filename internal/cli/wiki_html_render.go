package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// renderWikiHTML 生成单文件自包含 index.html（全量覆盖）。
func renderWikiHTML(repoAbs, outDir string, rc *wikiRenderCtx) error {
	data, cfg, cols, rels, freshNote, pkgs := rc.data, rc.cfg, rc.cols, rc.rels, rc.freshNote, rc.pkgs
	logger := zap.L()
	logger.Debug("enter renderWikiHTML", zap.Int("modules", len(data)))
	defer logger.Debug("exit renderWikiHTML")
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	meta, tableAlias, hidden := wikiMetaIndex(cfg)

	ordered := append([]*domain.WikiModule(nil), data...)
	sort.SliceStable(ordered, func(i, j int) bool {
		oi, oj := meta[ordered[i].Name].order, meta[ordered[j].Name].order
		if oi != oj {
			return oi != 0 && (oj == 0 || oi < oj)
		}
		return ordered[i].Name < ordered[j].Name
	})

	title := filepath.Base(repoAbs) + " 业务 wiki"
	var nav strings.Builder  // 左侧目录
	var main strings.Builder // 内容区
	for i, wm := range ordered {
		secID := fmt.Sprintf("sec-%d", i)
		modID := fmt.Sprintf("mod-%d", i)
		desc := meta[wm.Name].desc
		label := wm.Name
		if desc != "" {
			label += " — " + desc
		}

		nav.WriteString(fmt.Sprintf(
			`<li class="mod"><div class="mod-head fold-btn" data-target="%s" data-label="1">▸ %s</div><ul class="mod-sec" id="%s">`,
			secID, htmlEsc(label), secID))
		for _, a := range moduleAnchors(wm) {
			nav.WriteString(fmt.Sprintf(`<li><a href="#%s-%d">%s</a></li>`, a.key, i, a.label))
		}
		nav.WriteString("</ul></li>\n")

		main.WriteString(fmt.Sprintf(`<section id="%s"><h2>%s</h2>`, modID, htmlEsc(wm.Name)))
		if desc != "" {
			main.WriteString("<blockquote>" + htmlEsc(desc) + "</blockquote>")
		}
		main.WriteString(renderModuleHTML(wm, i, tableAlias, hidden, cfg, desc))
		main.WriteString("</section>\n")
	}

	if cfg.Architecture != "" {
		main.WriteString(`<section id="arch"><h2>架构图</h2><p class="muted">（来源：wiki.yaml architecture）</p><pre class="mermaid">` + htmlEsc(cfg.Architecture) + `</pre></section>` + "\n")
		nav.WriteString(`<li><a href="#arch">架构图</a></li>`)
	}

	hideTable := map[string]bool{}
	for _, t := range cfg.Tables {
		if t.Hidden {
			hideTable[t.Name] = true
		}
	}
	erMermaid := renderERMermaid(rels, hideTable)
	main.WriteString(`<section id="er"><h2>ER 图（表间关系）</h2>`)
	if strings.Contains(erMermaid, "||--") {
		main.WriteString(`<p class="muted">表间直接键关联（fk=值流验证的真实键 / query=WHERE 键关联），列级标注。字段定义见下方表清单。</p><pre class="mermaid">` + htmlEsc(erMermaid) + `</pre>`)
	} else {
		main.WriteString("<p class=\"muted\">（无表间直接关联）</p>")
	}
	main.WriteString("</section>\n")
	nav.WriteString(`<li><a href="#er">ER 图</a></li>`)

	tableCfgs := tableCfgsFrom(cfg)
	tables := collectTables(data, tableAlias, tableCfgs)
	main.WriteString(wikiTablesSectionHTML(tables, tableCfgs, cols))
	nav.WriteString(`<li><a href="#tables">表清单</a></li>`)
	// R1：命令/接口/包结构区块（单文件 html 全量包含）
	main.WriteString(renderCommandsHTML())
	nav.WriteString(`<li><a href="#commands">命令清单</a></li>`)
	main.WriteString(renderAPIHTML(repoAbs))
	nav.WriteString(`<li><a href="#api">HTTP 接口</a></li>`)
	if len(pkgs) > 0 {
		main.WriteString(renderPackagesHTML(pkgs))
		nav.WriteString(`<li><a href="#packages">包结构</a></li>`)
	}

	if len(cfg.Glossary) > 0 {
		main.WriteString(`<section id="glossary"><h2>术语表</h2>`)
		for _, g := range cfg.Glossary {
			main.WriteString(fmt.Sprintf("<p><strong>%s</strong>：%s</p>", htmlEsc(g.Term), htmlEsc(g.Definition)))
		}
		main.WriteString("</section>\n")
		nav.WriteString(`<li><a href="#glossary">术语表</a></li>`)
	}

	guide := `<strong>快速开始：</strong>① 看<a href="#arch">架构图</a>了解系统组成 → ② 按顺序读各模块（职责 → 入口 → 核心符号 → 相关表）→ ③ 查<a href="#tables">表清单</a>看字段与建表语句。`
	html := wikiHTMLPage(title, cfg.Project.Description, guide, nav.String(), main.String(), wikiPageOpts{freshNote: freshNote})
	return os.WriteFile(filepath.Join(outDir, "index.html"), []byte(html), 0o644)
}

// htmlEsc HTML 转义。
func htmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// containsStr 字符串切片包含判断（cli 包本地版）。
func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
