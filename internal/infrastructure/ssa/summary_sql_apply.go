package ssa

import (
	"fmt"
	"go/constant"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// applySQLSummary 处理 SQL 语句调用（Q97）：SQL 字符串（第 0 实参）解析
// 表名与列名 → 虚拟节点（Name=表.列）；后续值实参按 ? 顺序映射列，
// 发 summary_io 边（字段值 → 虚拟节点）。
// applySQLSummary 处理 SQL 语句摘要：SQL 字符串在 Args[sqlArg]（database/sql
// 的 receiver 后 Args[1]；gof Connector 接口无 receiver 在 Args[0]，Q158），
// 值实参在 sqlArg+1 起（variadic 解包按 ?/$N 顺序映射）。
func (ext *fieldExtractor) applySQLSummary(cc *ssa.CallCommon, calleeID domain.CanonicalID, spec summarySpec, callVal ssa.Value, sqlArg int) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).applySQLSummary")
	defer logger.Debug("exit (fieldExtractor).applySQLSummary")
	if sqlArg < 0 || sqlArg >= len(cc.Args) {
		return nil
	}

	sqlCands := ext.resolveSQLCandidates(cc.Args[sqlArg], 0)
	if len(sqlCands) == 0 {

		if c, ok := unwrapConst(cc.Args[sqlArg]); ok {
			sqlCands = []string{constant.StringVal(c.Value)}
		}
	}
	if len(sqlCands) == 0 {
		return nil
	}
	for _, sqlStr := range sqlCands {
		if err := ext.applySQLSummaryOne(cc, calleeID, spec, callVal, sqlArg, sqlStr); err != nil {
			return err
		}
	}
	return nil
}

// applySQLSummaryOne 单条 SQL 的摘要主体（Q252：多候选各自调用）。
func (ext *fieldExtractor) applySQLSummaryOne(cc *ssa.CallCommon, calleeID domain.CanonicalID, spec summarySpec, callVal ssa.Value, sqlArg int, sqlStr string) error {
	table, tableAlias, cols, whereCols, joinPairs := parseSQLStmt(sqlStr)
	line := ext.prog.Fset.PositionFor(cc.Pos(), false).Line

	if sqlStmtIsWrite(sqlStr) {
		spec.SQLWrite = true
	}

	if !spec.SQLWrite {
		return ext.applySQLRead(cc, spec, callVal, sqlArg, sqlStr, table, tableAlias, cols, whereCols, joinPairs, line)
	}

	access := "write"

	if table != "" {
		tableID := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + table + "." + access + "@" + fmt.Sprintf("%d", line))
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID:        tableID,
			Kind:      domain.KindFieldAccess,
			Name:      table,
			FilePath:  ext.currentFile,
			LineStart: line,
			Properties: map[string]any{
				"full_path":     table,
				"instance_path": table,
				"access_kind":   access,
				"code_snippet":  sqlStr,
				"type_string":   "sql",
				"is_external":   "true",
				"func_id":       string(ext.funcID),
			},
		}}); err != nil {
			return err
		}
	}
	values := []ssa.Value{}
	for i := sqlArg + 1; i < len(cc.Args); i++ {
		values = append(values, variadicElems(cc.Args[i])...)
	}

	if len(values) == 0 {
		for _, col := range cols {
			if col == "" || !validSQLColumn(col) {
				continue
			}
			name := table + "." + col
			id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + "." + access + "@" + fmt.Sprintf("%d", line))
			if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
				ID:        id,
				Kind:      domain.KindFieldAccess,
				Name:      name,
				FilePath:  ext.currentFile,
				LineStart: line,
				Properties: map[string]any{
					"full_path":     name,
					"instance_path": name,
					"access_kind":   access,
					"code_snippet":  sqlStr,
					"type_string":   "sql",
					"is_external":   "true",
					"func_id":       string(ext.funcID),
				},
			}}); err != nil {
				return err
			}
		}
	}
	for i, arg := range values {
		col := ""
		if i < len(cols) {
			col = cols[i]
		}

		if col != "" && !validSQLColumn(col) {
			continue
		}
		name := table
		if col != "" {
			name = table + "." + col
		}
		if name == "" {
			continue
		}
		realArg := arg
		if mi, ok := arg.(*ssa.MakeInterface); ok {
			realArg = mi.X
		}
		argID, err := ext.emitValue(realArg)
		if err != nil || argID == "" {
			continue
		}
		id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + "." + access + "@" + fmt.Sprintf("%d", line))
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID:        id,
			Kind:      domain.KindFieldAccess,
			Name:      name,
			FilePath:  ext.currentFile,
			LineStart: line,
			Properties: map[string]any{
				"full_path":     name,
				"instance_path": name,
				"access_kind":   access,
				"code_snippet":  sqlStr,
				"type_string":   "sql",
				"is_external":   "true",
				"func_id":       string(ext.funcID),
			},
		}}); err != nil {
			return err
		}
		if err := ext.emitEdgeKindLine(argID, id, domain.FactSummaryIO, line); err != nil {
			return err
		}
	}

	if len(whereCols) > 0 {
		for i, col := range whereCols {
			vi := len(cols) + i
			if vi >= len(values) {
				break
			}
			col = sqlColUnqual(table, tableAlias, col)
			if !validSQLColumn(col) {
				continue
			}
			name := table + "." + col
			id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + ".filter@" + fmt.Sprintf("%d", line))
			if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
				ID:        id,
				Kind:      domain.KindFieldAccess,
				Name:      name,
				FilePath:  ext.currentFile,
				LineStart: line,
				Properties: map[string]any{
					"full_path":     name,
					"instance_path": name,
					"access_kind":   "filter",
					"code_snippet":  sqlStr,
					"type_string":   "sql",
					"is_external":   "true",
					"func_id":       string(ext.funcID),
				},
			}}); err != nil {
				return err
			}
			realArg := values[vi]
			if mi, ok := realArg.(*ssa.MakeInterface); ok {
				realArg = mi.X
			}
			argID, err := ext.emitValue(realArg)
			if err != nil || argID == "" {
				continue
			}
			if err := ext.emitEdgeKindLine(argID, id, domain.FactSummaryIO, line); err != nil {
				return err
			}
		}
	}
	return nil
}
