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
	acts, data, cfg, cols, rels, freshNote, pkgs, degradeStats := rc.acts, rc.data, rc.cfg, rc.cols, rc.rels, rc.freshNote, rc.pkgs, rc.degradeStats
	logger := zap.L()
	logger.Debug("enter renderWikiHTML", zap.Int("modules", len(data)))
	defer logger.Debug("exit renderWikiHTML")
	if err := cleanWikiOutDir(outDir, data); err != nil {
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

	eg, egErr := acts.Entities() // R9：实体协作图（模块页/概览渲染）
	_ = egErr
	schemas := wikiSchemas(acts) // R19 表 schema 事实源（列类型/默认值）
	ormStructs := scanORMStructs(repoAbs) // R20 表关联结构体
	goTypes := ormColTypes(ormStructs) // R21 结构体 Go 类型 fallback

	title := filepath.Base(repoAbs) + " 业务 wiki"
	var nav strings.Builder  // 左侧目录
	var main strings.Builder // 内容区
	archMermaid := cfg.Architecture
	archNote := "（来源：wiki.yaml architecture）"
	if archMermaid == "" {
		archMermaid = archMermaidFallback(data, rc.cfg.Domains, rc.repo)
		archNote = "（自动生成：接入层→领域→存储层三层架构——yaml architecture 可覆盖）"
	}
	if archMermaid != "" {
		main.WriteString(`<section id="arch"><h2>架构图</h2><p class="muted">` + archNote + `</p>` + rc.diagramHTML(archMermaid) + `</section>` + "\n")
		nav.WriteString(`<li><a href="#arch">架构图</a></li>`)
	}
	// R7：AI 整理架构图（过滤基础包 + 分层分组）
	if curated := archMermaidCurated(data); curated != "" {
		main.WriteString(`<section id="arch-curated"><h2>架构图（AI 整理）</h2><p class="muted">过滤基础工具包（logging 等）+ 临时包（seed），分层分组（入口/核心/支撑）。</p>` + rc.diagramHTML(curated) + `</section>` + "\n")
		nav.WriteString(`<li><a href="#arch-curated">架构图（AI 整理）</a></li>`)
	}

	// R9：实体协作区块（对象设计视角）
	if sec := renderEntitiesSectionHTML(eg, rc); sec != "" {
		main.WriteString(sec)
		nav.WriteString(`<li><a href="#entities">实体协作</a></li>`)
	}
	// R14：核心业务流程图（yaml flows 手写——AI 只排位不生成内容）
	if sec := renderBusinessFlowsSectionHTML(cfg, rc); sec != "" {
		main.WriteString(sec)
		nav.WriteString(`<li><a href="#flows">核心业务流程图</a></li>`)
	}

	// R1：命令/接口区块（单文件 html 全量包含——怎么用，靠前）
	main.WriteString(renderCommandsHTML(acts, rc.repo, rc.RepoAbs))
	nav.WriteString(`<li><a href="#commands">命令清单</a></li>`)
	main.WriteString(renderAPIHTML(repoAbs))
	nav.WriteString(`<li><a href="#api">HTTP 接口</a></li>`)
	// R45：外部系统接口调用（grpc/http 调用但接口未在本项目定义）
	if ext := renderExternalInterfacesHTML(rc.repo); ext != "" {
		main.WriteString(ext)
		nav.WriteString(`<li><a href="#external">外部接口调用</a></li>`)
	}
	// R47：kafka topic 分类（生产/消费归属三分类）
	if kt := renderKafkaTopicsHTML(rc.repo); kt != "" {
		main.WriteString(kt)
		nav.WriteString(`<li><a href="#kafka">Kafka Topic</a></li>`)
	}
	// R2：系统流程区块（进程视角）
	main.WriteString(renderProcessesHTML(rc))
	nav.WriteString(`<li><a href="#processes">系统流程</a></li>`)
	// R40（用户要求）：gRPC 服务流程内容内嵌进 index.html 单文件——
	// 不再写独立子页（所有东西都在一个文件里）；md 模式仍多文件
	// R5：枚举与工具函数区块（AI 权威值）
	main.WriteString(renderEnumsHTML(repoAbs))
	nav.WriteString(`<li><a href="#enums">枚举与工具函数</a></li>`)
	if len(pkgs) > 0 {
		main.WriteString(renderPackagesHTML(pkgs, rc.repo))
		nav.WriteString(`<li><a href="#packages">包结构</a></li>`)
	}
	// R14：ER 图与表清单同属数据层，归位在实现细节区（认知路径靠后）
	tableCfgs := tableCfgsFrom(cfg)
	tables := collectTables(data, tableAlias, tableCfgs)
	hideTable := map[string]bool{}
	for _, t := range cfg.Tables {
		if t.Hidden {
			hideTable[t.Name] = true
		}
	}
	erMermaid := renderERMermaid(rels, hideTable)
	main.WriteString(`<section id="er"><h2>ER 图（表间关系）</h2>`)
	if strings.Contains(erMermaid, "||--") {
		main.WriteString(`<p class="muted">表间直接键关联（fk=值流验证的真实键 / query=WHERE 键关联），列级标注。字段定义见下方表清单。</p>` + rc.diagramHTML(erMermaid))
	} else {
		main.WriteString("<p class=\"muted\">（无表间直接关联）</p>")
	}
	main.WriteString("</section>\n")
	// R33：按业务领域分组（领域间图 + 每领域内部图）
	if sec := renderERDomainsHTML(rels, hideTable, rc); sec != "" {
		main.WriteString(sec)
		nav.WriteString(`<li><a href="#er-domains">ER 图（领域分组）</a></li>`)
	}
	nav.WriteString(`<li><a href="#er">ER 图</a></li>`)
	main.WriteString(wikiTablesSectionHTML(tables, tableCfgs, cols, schemas, ormStructs, goTypes))
	nav.WriteString(`<li><a href="#tables">表清单</a></li>`)

	if len(cfg.Glossary) > 0 {
		main.WriteString(`<section id="glossary"><h2>术语表</h2>`)
		for _, g := range cfg.Glossary {
			main.WriteString(fmt.Sprintf("<p><strong>%s</strong>：%s</p>", htmlEsc(g.Term), htmlEsc(g.Definition)))
		}
		main.WriteString("</section>\n")
		nav.WriteString(`<li><a href="#glossary">术语表</a></li>`)
	}
	// R14：模块（逐模块深入——认知路径靠后，比命令/流程细节深）
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
		main.WriteString(renderModuleHTML(wm, i, eg, wikiModuleKeyFlows(acts, wm), tableAlias, hidden, cfg, desc, rc))
		main.WriteString("</section>\n")
	}

	// R6：构建 SQL 解析降级统计（与 md/serve 三通道一致）
	if degradeStats != "" {
		main.WriteString(`<section id="degrade"><h2>构建 SQL 解析降级统计</h2><p class="muted">` + htmlEsc(">"+degradeStats) + `（AST 降级率异常高时检查解析器）</p></section>` + "\n")
		nav.WriteString(`<li><a href="#degrade">构建降级统计</a></li>`)
	}
	guide := `<strong>快速开始：</strong>① 看<a href="#arch">架构图</a>了解系统组成 → ② 看<a href="#entities">实体协作</a>了解对象怎么协作 → ③ 看<a href="#commands">命令清单</a>上手 → ④ 深入<a href="#mod-0">模块</a>与<a href="#tables">表</a>。`
	html := wikiHTMLPage(title, cfg.Project.Description, guide, nav.String(), main.String(), wikiPageOpts{freshNote: freshNote, diagram: rc.Diagram})
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
