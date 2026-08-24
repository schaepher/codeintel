package cli

// wiki --ai 的 AI 返回解析与 cfg 同步（从 wiki_ai.go 拆出——行数治理）。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"gopkg.in/yaml.v3"
)

// stripYAMLFence 剥离 AI 输出的 ```yaml 围栏（缺尾围栏也容忍）。
func stripYAMLFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	lines = lines[1:]
	if n := len(lines); n > 0 && strings.HasPrefix(strings.TrimSpace(lines[n-1]), "```") {
		lines = lines[:n-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}




// wikiBatchOut AI 批量返回结构（一次请求处理全部缺口）。
type wikiBatchOut struct {
	Modules []struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	} `yaml:"modules"`
	Tables []struct {
		Name    string `yaml:"name"`
		Alias   string `yaml:"alias"`
		Columns []struct {
			Name    string `yaml:"name"`
			Comment string `yaml:"comment"`
		} `yaml:"columns"`
	} `yaml:"tables"`
	Glossary []struct {
		Term       string `yaml:"term"`
		Definition string `yaml:"definition"`
	} `yaml:"glossary"`
}

// wikiQAReferences 历史问答参考资料（--with-qa）：按缺口表名/模块
// 短名匹配 qa_history（context/question LIKE），最多 5 条。
func wikiQAReferences(repo *sqlite.Repo, mods []aiModuleGap, tbls []aiTableGap, colGaps []aiColGap) []string {
	if repo == nil {
		return nil
	}
	var kw []string
	for _, m := range mods {
		short := m.name
		if i := strings.LastIndex(short, "/"); i >= 0 {
			short = short[i+1:]
		}
		kw = append(kw, short)
	}
	for _, t := range tbls {
		kw = append(kw, t.name)
	}
	for _, g := range colGaps {
		kw = append(kw, g.table)
	}
	recs, err := repo.QAForSymbols(kw, 5)
	if err != nil || len(recs) == 0 {
		return nil
	}
	var out []string
	for _, r := range recs {
		out = append(out, fmt.Sprintf("Q: %s\nA: %s", r.Question, r.Answer))
	}
	return out
}

// wikiAIBatchPrompt 批量缺口 prompt：模块描述 + 表别名 + 列说明 +
// 术语表一次请求全部带上（省调用次数、AI 上下文连贯）。rels 提供
// 表间关联事实（列说明的读写上下文）；qaRefs 为历史问答参考资料
// （--with-qa，可选）。
func wikiAIBatchPrompt(mods []aiModuleGap, tbls []aiTableGap, colGaps []aiColGap, rels []*domain.TableRelation, qaRefs []string) string {
	var b strings.Builder
	b.WriteString("你是代码仓库文档助手。根据以下代码事实，为缺失内容生成中文描述。一次全部处理。\n\n")
	if len(mods) > 0 {
		b.WriteString("一、模块职责描述（每个一句话，<=30 字）：\n")
		for _, g := range mods {
			fmt.Fprintf(&b, "- %s", g.name)
			if g.pkgDesc != "" {
				fmt.Fprintf(&b, "（包注释: %s", g.pkgDesc)
				if g.symbols != "" {
					fmt.Fprintf(&b, "；核心符号: %s", g.symbols)
				}
				b.WriteString("）")
			} else if g.symbols != "" {
				fmt.Fprintf(&b, "（核心符号: %s）", g.symbols)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(tbls) > 0 {
		b.WriteString("二、表中文别名（每表一个，<=10 字）：\n")
		for _, g := range tbls {
			fmt.Fprintf(&b, "- %s", g.name)
			if g.cols != "" {
				fmt.Fprintf(&b, "（列: %s）", g.cols)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(colGaps) > 0 {
		b.WriteString("三、表列中文说明（每列一句话）：\n")
		for _, g := range colGaps {
			fmt.Fprintf(&b, "- %s: %s", g.table, strings.Join(g.cols, ", "))
			if r := colGapRels(rels, g.table); r != "" {
				fmt.Fprintf(&b, "（关联: %s）", r)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(qaRefs) > 0 {
		b.WriteString("五、历史问答参考（用户与 AI 关于本项目的问答记录，含背景信息——回答时可参考，不要照抄）：\n")
		for _, r := range qaRefs {
			b.WriteString(r + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(`四、术语表：从以上模块/表/列事实中识别 3-8 个专业术语（缩写/内部黑话，如 ssa/ast/ER/MCP），给出中文定义。
只输出 YAML（结构严格如下，缺哪个部分就省略哪个）：
modules:
  - name: <模块名>
    description: <描述>
tables:
  - name: <表名>
    alias: <别名>
    columns:
      - name: <列名>
        comment: <说明>
glossary:
  - term: <术语>
    definition: <定义>`)
	return b.String()
}

// colGapRels 表相关关联摘要（列说明的读写上下文：A.x → B.y (类型)）。
func colGapRels(rels []*domain.TableRelation, table string) string {
	var parts []string
	for _, r := range rels {
		if r.FromTable == table {
			parts = append(parts, fmt.Sprintf("%s.%s → %s.%s (%s)", r.FromTable, r.FromCol, r.ToTable, r.ToCol, r.Type))
		} else if r.ToTable == table {
			parts = append(parts, fmt.Sprintf("%s.%s → %s.%s (%s)", r.FromTable, r.FromCol, r.ToTable, r.ToCol, r.Type))
		}
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, ", ")
}

// parseWikiBatch 解析 AI 批量返回 → wikiBatchOut（围栏剥离 + 空结果
// 校验——空结果视为不可解析，触发重试）。
func parseWikiBatch(s string) (wikiBatchOut, error) {
	s = stripYAMLFence(s)
	var out wikiBatchOut
	if err := yaml.Unmarshal([]byte(s), &out); err != nil {
		return out, fmt.Errorf("AI 批量返回不可解析: %v", err)
	}
	if len(out.Modules)+len(out.Tables) == 0 {
		return out, fmt.Errorf("AI 批量返回为空")
	}
	return out, nil
}

// cfgSetGlossary 同步更新 cfg.Glossary（渲染用）。
func cfgSetGlossary(cfg *wikiConfig, term, def string) {
	for i := range cfg.Glossary {
		if cfg.Glossary[i].Term == term {
			cfg.Glossary[i].Definition = def
			return
		}
	}
	cfg.Glossary = append(cfg.Glossary, wikiGlossaryItem{Term: term, Definition: def})
}

// cfgSetModuleDesc 同步更新 cfg.Modules（渲染用）。
func cfgSetModuleDesc(cfg *wikiConfig, name, desc string) {
	for i := range cfg.Modules {
		if cfg.Modules[i].Name == name {
			cfg.Modules[i].Description = desc
			return
		}
	}
	cfg.Modules = append(cfg.Modules, wikiModuleCfg{Name: name, Description: desc})
}

// cfgSetTableAlias 同步更新 cfg.Tables（渲染用）。
func cfgSetTableAlias(cfg *wikiConfig, name, alias string) {
	for i := range cfg.Tables {
		if cfg.Tables[i].Name == name {
			cfg.Tables[i].Alias = alias
			return
		}
	}
	cfg.Tables = append(cfg.Tables, wikiTableConfig{Name: name, Alias: alias})
}

// cfgSetColumnComments 同步更新 cfg.Tables 的列说明（渲染用）。
func cfgSetColumnComments(cfg *wikiConfig, tbl string, comments map[string]string) {
	ti := -1
	for i := range cfg.Tables {
		if cfg.Tables[i].Name == tbl {
			ti = i
			break
		}
	}
	if ti < 0 {
		cfg.Tables = append(cfg.Tables, wikiTableConfig{Name: tbl})
		ti = len(cfg.Tables) - 1
	}
	for col, comment := range comments {
		found := false
		for j := range cfg.Tables[ti].Columns {
			if cfg.Tables[ti].Columns[j].Name == col {
				cfg.Tables[ti].Columns[j].Comment = comment
				found = true
				break
			}
		}
		if !found {
			cfg.Tables[ti].Columns = append(cfg.Tables[ti].Columns, wikiTableColumn{Name: col, Comment: comment})
		}
	}
}
