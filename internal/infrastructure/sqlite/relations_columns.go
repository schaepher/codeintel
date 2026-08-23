package sqlite

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetTableColumns 按表名聚合列虚拟节点（query table）：Name=表（整表行）
// 或 表.列（Q97 持久化映射）；每列带写入方（summary_io 入边 source 值节点
// 的所属函数与行号）。读取方（出边）通常为空——SELECT 读路径未解析。
func (r *Repo) GetTableColumns(table string) ([]*domain.TableColumn, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetTableColumns")
	defer logger.Debug("exit (Repo).GetTableColumns")
	rows, err := r.Query(`SELECT id, name, line_start, properties
		FROM nodes WHERE kind = 'field_access'
		  AND json_extract(properties, '$.is_external') = 'true'
		  AND (name = ? OR name LIKE ?)
		ORDER BY name, id`, table, table+".%")
	if err != nil {
		return nil, err
	}
	// 先收完外层行再关（SQLite 单连接：迭代中开新 Query 会挂起）
	type rowT struct {
		id, name, access string
		line             int
	}
	var raw []rowT
	for rows.Next() {
		var id, name, props string
		var line int
		if err := rows.Scan(&id, &name, &line, &props); err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(props), &m); err != nil {
			return nil, err
		}
		access := ""
		if a, ok := m["access_kind"].(string); ok {
			access = a
		}
		raw = append(raw, rowT{id: id, name: name, access: access, line: line})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	cols := map[string]*domain.TableColumn{}
	var order []string
	for _, rt := range raw {
		col, ok := cols[rt.name]
		if !ok {
			col = &domain.TableColumn{Name: rt.name, Access: rt.access, LineStart: rt.line}
			cols[rt.name] = col
			order = append(order, rt.name)
		}

		ws, err := r.Query(`SELECT source_id, json_extract(metadata, '$.line_num')
			FROM edges WHERE target_id = ? AND kind = 'summary_io'`, rt.id)
		if err != nil {
			return nil, err
		}
		for ws.Next() {
			var src string
			var ln sql.NullFloat64
			if err := ws.Scan(&src, &ln); err != nil {
				ws.Close()
				return nil, err
			}

			funcID := src
			if i := strings.LastIndex(src, "#"); i >= 0 {
				funcID = src[:i]
			}

			line := rt.line
			if ln.Valid {
				line = int(ln.Float64)
			}
			col.Writers = append(col.Writers, domain.TableEndpoint{
				FuncID:   funcID,
				FuncName: shortNameFromID(funcID),
				Line:     line,
			})
		}
		ws.Close()

		if rt.access == "read" {
			rs, err := r.Query(`SELECT target_id, json_extract(metadata, '$.line_num')
				FROM edges WHERE source_id = ? AND kind = 'summary_io'`, rt.id)
			if err != nil {
				return nil, err
			}
			for rs.Next() {
				var tgt string
				var ln sql.NullFloat64
				if err := rs.Scan(&tgt, &ln); err != nil {
					rs.Close()
					return nil, err
				}
				funcID := tgt
				if i := strings.LastIndex(tgt, "#"); i >= 0 {
					funcID = tgt[:i]
				}
				line := rt.line
				if ln.Valid {
					line = int(ln.Float64)
				}
				col.Readers = append(col.Readers, domain.TableEndpoint{
					FuncID:   funcID,
					FuncName: shortNameFromID(funcID),
					Line:     line,
				})
			}
			rs.Close()
		}
	}
	out := make([]*domain.TableColumn, 0, len(order))
	for _, name := range order {
		out = append(out, cols[name])
	}
	return out, nil
}

// GetAllTableColumns ER 图列数据源（/api/er）：一次查询全库外部表列
// （表.列 形态，过滤与 GetTables 一致：is_external + gorm/sql/xorm），
// 按列名排序去重（同名列多节点首个保留）。不带 writers/readers 明细
// （ER 图不需要，避免逐表 N+1 查询）。
func (r *Repo) GetAllTableColumns() ([]*domain.TableColumn, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetAllTableColumns")
	defer logger.Debug("exit (Repo).GetAllTableColumns")
	rows, err := r.Query(`SELECT name, line_start, properties,
		COALESCE(json_extract(properties, '$.col_type'), '') FROM nodes
		WHERE kind = 'field_access'
		  AND json_extract(properties, '$.is_external') = 'true'
		  AND json_extract(properties, '$.type_string') IN ('gorm', 'sql', 'xorm')
		  AND name LIKE '%.%' AND name NOT LIKE '%.%.%'
		ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []*domain.TableColumn
	for rows.Next() {
		var name, props, colType string
		var line int
		if err := rows.Scan(&name, &line, &props, &colType); err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if !validTableColumn(name) {
			continue // #247 SQL 截断/关键字/字面量噪音
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(props), &m); err != nil {
			return nil, err
		}
		access := ""
		if a, ok := m["access_kind"].(string); ok {
			access = a
		}
		out = append(out, &domain.TableColumn{Name: name, ColType: colType, Access: access, LineStart: line})
	}
	return out, rows.Err()
}

// validTableColumn 表列名合法性（#247）：「表.列」两部分均为合法
// 标识符（字母开头 + 非 SQL 关键字）——与 ssa validSQLColumn 同规则，
// 查询层兜底（旧库已落噪音节点）。
func validTableColumn(name string) bool {
	dot := strings.Index(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return false
	}
	return validColPart(name[:dot]) && validColPart(name[dot+1:])
}

// validColPart 单段标识符校验（与 ssa.validSQLColumn 同规则）。
func validColPart(s string) bool {
	if s == "" {
		return false
	}
	c0 := s[0]
	if !(c0 == '_' || ('a' <= c0 && c0 <= 'z') || ('A' <= c0 && c0 <= 'Z')) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		ok := c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
		if !ok {
			return false
		}
	}
	l := strings.ToLower(s)
	for kw := range sqlKeywords {
		if l == kw {
			return false
		}
	}
	return true
}

// sqlKeywords 关键字黑名单（查询层兜底版）。
var sqlKeywords = map[string]bool{
	"select": true, "from": true, "where": true, "and": true, "or": true,
	"not": true, "in": true, "on": true, "join": true, "left": true,
	"right": true, "inner": true, "outer": true, "limit": true, "offset": true,
	"order": true, "group": true, "by": true, "having": true, "distinct": true,
	"as": true, "case": true, "when": true, "then": true, "else": true,
	"end": true, "null": true, "true": true, "false": true, "count": true,
	"sum": true, "avg": true, "max": true, "min": true, "exists": true,
	"like": true, "between": true, "is": true, "desc": true, "asc": true,
}
