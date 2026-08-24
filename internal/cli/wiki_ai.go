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

	"github.com/schaepher/codeintel/internal/domain"
)

// aiTimeout 单条补缺超时（超时跳过该条）。真实 claude JSON 模式
// 响应可超 60s（2026-08-24 实测），120s 留余量。
const aiTimeout = 120 * time.Second

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
			tbls = append(tbls, aiTableGap{name: t.name, cols: strings.Join(colByTbl[t.name], ", ")})
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

// aiBatchMax 单批缺口上限（超过分两批：模块+表别名 / 列说明——
// 同会话 resume，AI 保留第一批上下文）。
const aiBatchMax = 60

// wikiAIFill 执行 --ai 补缺：缺口收集 → 批量一次请求（缺口合并进
// 单个 prompt，AI 一次返回完整 YAML）→ 合并 wiki.yaml。
// 返回 成功/跳过/失败 计数；*cfg 同步更新（渲染用）。
func wikiAIFill(yamlPath string, cfg *wikiConfig, data []*domain.WikiModule, cols []*domain.TableColumn, rels []*domain.TableRelation, agent string, timeout time.Duration) (ok, skip, fail int) {
	_ = rels // 当前 prompt 用列名+类型事实；关联关系后续可扩展
	mods, tbls, colGaps := wikiAIGaps(data, *cfg, cols)
	if len(mods)+len(tbls)+len(colGaps) == 0 {
		return 0, 0, 0
	}
	e, err := loadYAMLEditor(yamlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 0, 0, 0
	}
	apply := func(out wikiBatchOut) {
		for _, m := range out.Modules {
			e.setModuleDesc(m.Name, m.Description)
			cfgSetModuleDesc(cfg, m.Name, m.Description)
			ok++
		}
		for _, t := range out.Tables {
			if t.Alias != "" {
				e.setTableAlias(t.Name, t.Alias)
				cfgSetTableAlias(cfg, t.Name, t.Alias)
				ok++
			}
			if len(t.Columns) > 0 {
				comments := map[string]string{}
				for _, c := range t.Columns {
					comments[c.Name] = c.Comment
				}
				e.setColumnComments(t.Name, comments)
				cfgSetColumnComments(cfg, t.Name, comments)
				ok++
			}
		}
	}
	first := wikiAIBatchPrompt(mods, tbls, colGaps)
	out, err := aiCallOnce(agent, first, timeout, parseWikiBatch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: AI 批量补缺失败: %v\n", err)
		return 0, 0, len(mods) + len(tbls) + len(colGaps)
	}
	apply(out)
	if ok > 0 {
		if err := e.save(yamlPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: 写回 %s: %v\n", yamlPath, err)
		}
	}
	return ok, skip, fail
}

// aiCallOnce 调 AI 并解析：运行失败（CLI 缺失/超时）直接报错跳过；
// 解析失败重试一次（泛型——desc/alias 返回 string，列说明返回 map）。
func aiCallOnce[T any](agent, prompt string, timeout time.Duration, parse func(string) (T, error)) (T, error) {
	var zero T
	resp, err := agentRunner(agent, prompt, timeout)
	if err != nil {
		return zero, err
	}
	v, err := parse(resp)
	if err == nil {
		return v, nil
	}
	resp2, err2 := agentRunner(agent, prompt, timeout)
	if err2 != nil {
		return zero, err2
	}
	return parse(resp2)
}



