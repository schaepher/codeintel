package cli

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// erNameRe 非安全 mermaid 实体名字符（表名清洗用——动态表名含 % 等）。
var erNameRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// wikiRelations 获取全库表间关联（Q251 ER 图页面）：优先复用已算
// relation_candidates；未算（ErrRelationInProgress）时同步兜底计算
// （wiki 是批处理命令，直接等结果；serve 的异步兜底不适合）。
func wikiRelations(acts *action.Actions) ([]*domain.TableRelation, error) {
	rels, err := acts.RelationsAll("")
	if err == nil {
		return rels, nil
	}
	if !errors.Is(err, domain.ErrRelationInProgress) {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "正在计算表间关系（ER 图首次生成需要）…\n")
	if err := acts.PrecomputeAllRelations(nil); err != nil {
		return nil, err
	}
	rels, err = acts.RelationsAll("")
	if err != nil {

		fmt.Fprintf(os.Stderr, "warning: ER 图关系数据不可用: %v\n", err)
		return nil, nil
	}
	return rels, nil
}

// renderERMermaid ER 图 mermaid（Q251）：表实体 + 关系线（仅直接键
// 关联 fk/query，列级标注 label；write/read 间接关联不画）。隐藏表
// 过滤；确定性排序。
// 表名经 erEntityName 清洗——动态表名（go2o 实测 pt_%s）含 % 是
// mermaid 语法错误，整块图解析挂掉。
func renderERMermaid(rels []*domain.TableRelation, hideTable map[string]bool) string {
	tables := map[string]bool{}
	var lines []string
	for _, r := range rels {
		if r.Type != domain.RelationFK && r.Type != domain.RelationQuery {
			continue
		}
		if hideTable[r.FromTable] || hideTable[r.ToTable] {
			continue
		}
		ft, tt := erEntityName(r.FromTable), erEntityName(r.ToTable)
		tables[ft] = true
		tables[tt] = true
		lines = append(lines, fmt.Sprintf("    %s ||--o{ %s : \"%s → %s [%s]\"",
			ft, tt, r.FromCol, r.ToCol, r.Type))
	}
	if len(tables) == 0 {
		return "erDiagram\n"
	}
	var sb strings.Builder
	sb.WriteString("erDiagram\n")
	var names []string
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		sb.WriteString("    " + t + "\n")
	}
	sort.Strings(lines)
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
	return sb.String()
}

// erEntityName mermaid erDiagram 实体名清洗：非字母数字下划线 → 下划线。
// 列级 label 在引号内不受影响，不清洗。
func erEntityName(name string) string {
	return erNameRe.ReplaceAllString(name, "_")
}

// renderERPage ER 图页面（Q251，er.md）：erDiagram + 关系明细表
// （仅 fk/query，隐藏表过滤）。字段详情见 tables.md。
func renderERPage(rels []*domain.TableRelation, hideTable map[string]bool, rc *wikiRenderCtx) string {
	var b strings.Builder
	b.WriteString("# ER 图（表间关系）\n\n")
	b.WriteString("表间直接键关联（fk=值流验证的真实键 / query=WHERE 键关联），列级标注。字段定义与建表语句见[表清单](tables.md)。\n\n")
	m := renderERMermaid(rels, hideTable)
	if !strings.Contains(m, "||--") {
		b.WriteString("（无表间直接关联）\n\n")
	} else {
		b.WriteString(rc.diagramMD(m))
	}
	// R33：按业务领域分组（领域间图 + 每领域内部图）
	if sec := renderERDomainsMD(rels, hideTable, rc); sec != "" {
		b.WriteString(sec)
	}
	b.WriteString("## 关系明细\n\n")
	b.WriteString("| 本表 | 本表列 | 关联表 | 关联列 | 类型 |\n|---|---|---|---|---|\n")
	type row struct{ a, b, c, d, e string }
	var rows []row
	for _, r := range rels {
		if r.Type != domain.RelationFK && r.Type != domain.RelationQuery {
			continue
		}
		if hideTable[r.FromTable] || hideTable[r.ToTable] {
			continue
		}
		rows = append(rows, row{r.FromTable, r.FromCol, r.ToTable, r.ToCol, string(r.Type)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].a != rows[j].a {
			return rows[i].a < rows[j].a
		}
		if rows[i].c != rows[j].c {
			return rows[i].c < rows[j].c
		}
		return rows[i].b < rows[j].b
	})
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", r.a, r.b, r.c, r.d, r.e))
	}
	return b.String()
}
