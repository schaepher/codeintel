package cli

// #0 `codeintel wiki --ai`——增量补缺：缺口收集（无描述模块/无别名
// 表/无说明列）→ 逐条构建 prompt（列名+类型事实）→ AI 初稿 → 合并
// wiki.yaml（保留注释、标注 # AI 初稿，git diff 可回滚）。单条超时
// 跳过、解析失败重试一次，结尾报告 成功/跳过/失败 计数。

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// aiTimeout 单条补缺超时（超时跳过该条）。真实 claude JSON 模式
// 响应可超 60s（2026-08-24 实测），120s 留余量；go2o 第二批
// （30 组 ≈200 列名）实测稳定超 120s → 240s 双保险（配合批次下调）。
const aiTimeout = 240 * time.Second

// aiModuleGap 一个无描述模块（AI 需要的事实：包注释 + 核心符号线索）。
type aiModuleGap struct {
	name    string
	pkgDesc string
	symbols string
}

// aiTableGap 一张无别名表（AI 需要的事实：列名+类型）。
type aiTableGap struct {
	name string
	cols string
}

// aiColGap 一张表里缺说明的列（AI 需要的事实：表名 + 列名清单）。
type aiColGap struct {
	table string
	cols  []string
}

// wikiAIGaps 缺口收集：无描述模块/无别名表/无说明列（有内容的跳过）。
func wikiAIGaps(data []*domain.WikiModule, cfg wikiConfig, cols []*domain.TableColumn) (mods []aiModuleGap, tbls []aiTableGap, colGaps []aiColGap) {
	meta, tableAlias, _ := wikiMetaIndex(cfg)
	for _, wm := range data {
		if meta[wm.Name].desc != "" || wm.Desc != "" {
			continue
		}
		g := aiModuleGap{name: wm.Name, pkgDesc: wm.Desc}
		var syms []string
		for i, s := range wm.CoreSymbols {
			if i >= 5 {
				break
			}
			syms = append(syms, fmt.Sprintf("%s(调用者 %d)", s.Name, s.Callers))
		}
		g.symbols = strings.Join(syms, ", ")
		mods = append(mods, g)
	}
	tableCfgs := tableCfgsFrom(cfg)
	colByTbl := map[string][]string{}
	for _, c := range cols {
		if i := strings.Index(c.Name, "."); i > 0 {
			colByTbl[c.Name[:i]] = append(colByTbl[c.Name[:i]], c.Name[i+1:]+colTypeSuffix(c))
		}
	}
	for _, t := range collectTables(data, tableAlias, tableCfgs) {
		if t.alias == "" {
			tbls = append(tbls, aiTableGap{name: t.name, cols: tableColBrief(colByTbl[t.name])})
		}
		var missing []string
		tc := tableCfgs[t.name]
		for _, r := range mergeTableColumnsWithSchema(t.name, cols, tc.Columns, nil, nil) {
			if r.comment == "" {
				missing = append(missing, r.name)
			}
		}
		if len(missing) > 0 {
			colGaps = append(colGaps, aiColGap{table: t.name, cols: missing})
		}
	}
	return
}

// colTypeSuffix 列名 + 类型后缀（有类型时 "（INTEGER）"）。
func colTypeSuffix(c *domain.TableColumn) string {
	if c.ColType == "" {
		return ""
	}
	return "(" + c.ColType + ")"
}

// tableColBrief 表列清单摘要：前 10 列 + 省略号——表别名推断不需要
// 全列（go2o 实测 59 表 × 15 列 = 900 列名致 prompt 过大 AI 超时）。
func tableColBrief(cols []string) string {
	if len(cols) == 0 {
		return ""
	}
	if len(cols) <= 10 {
		return strings.Join(cols, ", ")
	}
	return strings.Join(cols[:10], ", ") + "…"
}

// aiBatchMax 单批缺口上限（模块/表/列组条数——超过按条数切片）。
// go2o 实测 60 表/批（每表 10 列截断 = 600 列名）仍超时 → 30 条/批
// （300 列名/批通过）；第二批（30 组 ≈200 列名）claude 生成仍稳定
// 超 120s → 降 20 条/批（≤200 列名），配合 aiTimeout 240s。
// aiBatchMaxCols 列名数上限（列组内列名多时 prompt 巨大——按列名数
// 切更稳）。
const (
	aiBatchMax     = 20
	aiBatchMaxCols = 200
)

// wikiAIFill 执行 --ai 补缺：缺口收集 → 批量一次请求（缺口合并进
// 单个 prompt，AI 一次返回完整 YAML）→ 合并 wiki.yaml。
// withQA 时从 qa_history 读取相关 Q&A 作为参考资料（W3）。
// 返回 成功/跳过/失败 计数；*cfg 同步更新（渲染用）。
func wikiAIFill(yamlPath string, cfg *wikiConfig, data []*domain.WikiModule, cols []*domain.TableColumn, rels []*domain.TableRelation, agent string, timeout time.Duration, withQA bool, acts *action.Actions, repoAbs string) (ok, skip, fail int) {
	mods, tbls, colGaps := wikiAIGaps(data, *cfg, cols)
	// R57：ai.fill.<类别>=off（wiki.yaml/全局）→ 该类别缺口剔除（整类跳过）
	if !aiEnabled("fill.modules", *cfg) {
		mods = nil
	}
	if !aiEnabled("fill.tables", *cfg) {
		tbls = nil
	}
	if !aiEnabled("fill.columns", *cfg) {
		colGaps = nil
	}
	if len(mods)+len(tbls)+len(colGaps) == 0 {
		return 0, 0, 0
	}
	e, err := action.LoadYAMLEditor(yamlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 0, 0, 0
	}
	// R57：apply 按类别开关过滤——off 类别即使 AI 返回也不写回
	apply := func(out wikiBatchOut) {
		if aiEnabled("fill.modules", *cfg) {
			for _, m := range out.Modules {
				e.SetModuleDesc(m.Name, m.Description)
				cfgSetModuleDesc(cfg, m.Name, m.Description)
				ok++
			}
		}
		if aiEnabled("fill.tables", *cfg) {
			for _, t := range out.Tables {
				if t.Alias != "" {
					e.SetTableAlias(t.Name, t.Alias)
					cfgSetTableAlias(cfg, t.Name, t.Alias)
					ok++
				}
			}
		}
		if aiEnabled("fill.columns", *cfg) {
			for _, t := range out.Tables {
				if len(t.Columns) > 0 {
					comments := map[string]string{}
					for _, c := range t.Columns {
						comments[c.Name] = c.Comment
					}
					e.SetColumnComments(t.Name, comments)
					cfgSetColumnComments(cfg, t.Name, comments)
					ok++
				}
			}
		}
		if aiEnabled("fill.glossary", *cfg) {
			for _, g := range out.Glossary {
				e.SetGlossary(g.Term, g.English, g.Abbr, g.Definition)
				cfgSetGlossary(cfg, g.Term, g.English, g.Abbr, g.Definition)
				ok++
			}
		}
	}
	// 分批（方案 A）：模块描述/表别名各一批（短文本——合并全部缺口，
	// 一次 AI 请求；原按条数切 aiBatchMax=20——go2o 148 表别名切成 8+
	// 批 → 8+ 次调用，用户预期一次）；列说明按列名数切批（长文本——
	// 每批累计 ≤ aiBatchMaxCols，go2o 1446 列防 prompt 爆炸；同会话
	// resume——AI 保留前批上下文）
	batches := splitGapBatches(mods, tbls, colGaps, aiBatchMaxCols)
	// 命令执行界面展示：缺口统计 + 调用计划（zap 未初始化是 noop——
	// 直接 stderr，用户实时可见）
	totalCols := 0
	for _, g := range colGaps {
		totalCols += len(g.cols)
	}
	fmt.Fprintf(os.Stderr, "[wiki --ai] 缺口：模块 %d、表别名 %d、列说明 %d 组/%d 列——%d 次 AI 调用\n",
		len(mods), len(tbls), len(colGaps), totalCols, len(batches))
	// W3：--with-qa——从历史问答读取相关 Q&A 作参考资料（按缺口
	// 表名/模块短名匹配，最多 5 条）
	var qaRefs []string
	if withQA {
		qaRefs = wikiQAReferences(acts, mods, tbls, colGaps)
	}
	// R57：ai.fill.glossary=off → prompt 不带术语表段（AI 不生成）
	withGlossary := aiEnabled("fill.glossary", *cfg)
	for i, b := range batches {
		desc := fmt.Sprintf("模块 %d、表别名 %d、列说明 %d 组", len(b.mods), len(b.tbls), len(b.colGaps))
		fmt.Fprintf(os.Stderr, "[wiki --ai] AI 调用 %d/%d 开始：%s\n", i+1, len(batches), desc)
		out, err := aiCallOnce(agent, wikiAIBatchPrompt(b.mods, b.tbls, b.colGaps, rels, qaRefs, withGlossary), timeout, repoAbs, parseWikiBatch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[wiki --ai] AI 调用 %d/%d 失败：%v\n", i+1, len(batches), err)
			fail += len(b.mods) + len(b.tbls) + len(b.colGaps)
			continue
		}
		apply(out)
		fmt.Fprintf(os.Stderr, "[wiki --ai] AI 调用 %d/%d 完成\n", i+1, len(batches))
	}
	fmt.Fprintf(os.Stderr, "[wiki --ai] 完成：补全 %d 条、跳过 %d、失败 %d\n", ok, skip, fail)
	if ok > 0 {
		if err := e.Save(yamlPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: 写回 %s: %v\n", yamlPath, err)
		}
	}
	return ok, skip, fail
}

// aiBatchGaps 一批的缺口（分块用）。
type aiBatchGaps struct {
	mods    []aiModuleGap
	tbls    []aiTableGap
	colGaps []aiColGap
}

// splitGapBatches 缺口分批：全部合并一批——一次 AI 调用（用户明确
// 预期；原按条数/列名切批——go2o 148 表别名 + 1446 列被切成 10+ 批
// → 10+ 次 AI 调用）。prompt 过大风险由 AI 调用侧承担（失败最多重试
// 一次，aiCallOnce 把上次失败结果带回重试 prompt）。
func splitGapBatches(mods []aiModuleGap, tbls []aiTableGap, colGaps []aiColGap, maxCols int) []aiBatchGaps {
	return []aiBatchGaps{{mods: mods, tbls: tbls, colGaps: colGaps}}
}

// aiCallOnce 调 AI 并解析：失败最多重试一次（用户要求）——运行失败
// （CLI 缺失/超时）直接报错跳过；解析失败重试一次，且把上次失败结果
// （解析错误 + 输出摘要）带回重试 prompt，AI 据此修正格式。
// dir：子进程 cwd（目标仓库根——agent 读仓库内文件免权限）。
func aiCallOnce[T any](agent, prompt string, timeout time.Duration, dir string, parse func(string) (T, error)) (T, error) {
	var zero T
	resp, err := agentRunner(agent, prompt, timeout, dir)
	if err != nil {
		return zero, err
	}
	v, err := parse(resp)
	if err == nil {
		return v, nil
	}
	// 重试一次：记录上次失败结果（解析错误 + 输出摘要 ≤2000 字符）
	failNote := err.Error()
	prev := resp
	if len(prev) > 2000 {
		prev = prev[:2000] + "…（截断）"
	}
	retryPrompt := prompt + "\n\n上次输出解析失败：" + failNote + "。请检查 YAML 格式（缩进/引号/字段名）后重新输出完整结果。\n上次输出：\n" + prev
	resp2, err2 := agentRunner(agent, retryPrompt, timeout, dir)
	if err2 != nil {
		return zero, err2
	}
	return parse(resp2)
}
