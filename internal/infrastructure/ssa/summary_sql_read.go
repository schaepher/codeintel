package ssa

import (
	"fmt"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/ssa"
)

// applySQLRead Q252 行数治理：applySQLSummaryOne 的读分支（表节点 +
// 列节点 + WHERE filter + JOIN 键对 emit）。
func (ext *fieldExtractor) applySQLRead(cc *ssa.CallCommon, spec summarySpec, callVal ssa.Value, sqlArg int, sqlStr string, table, tableAlias string, cols, whereCols []string, joinPairs []sqlJoinPair, line int) error {
	if table == "" {
		return nil
	}

	tableID := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + table + ".read@" + fmt.Sprintf("%d", line))
	if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
		ID:        tableID,
		Kind:      domain.KindFieldAccess,
		Name:      table,
		FilePath:  ext.currentFile,
		LineStart: line,
		Properties: map[string]any{
			"full_path":     table,
			"instance_path": table,
			"access_kind":   "read",
			"code_snippet":  sqlStr,
			"type_string":   "sql",
			"is_external":   "true",
			"func_id":       string(ext.funcID),
		},
	}}); err != nil {
		return err
	}
	var callID domain.CanonicalID

	if cb := callbackClosureParam(cc); cb != nil {
		callID, _ = ext.emitValue(cb)
	} else if callVal != nil {
		callID, _ = ext.emitValue(callVal)
	}
	if len(cols) == 0 {

		if callID != "" {
			if err := ext.emitEdgeKindLine(tableID, domain.CanonicalID(callID), domain.FactSummaryIO, line); err != nil {
				return err
			}
		}
	}
	for _, col := range cols {
		if col == "" || !validSQLColumn(col) {
			continue
		}
		name := table + "." + col
		id := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + name + ".read@" + fmt.Sprintf("%d", line))
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID:        id,
			Kind:      domain.KindFieldAccess,
			Name:      name,
			FilePath:  ext.currentFile,
			LineStart: line,
			Properties: map[string]any{
				"full_path":     name,
				"instance_path": name,
				"access_kind":   "read",
				"code_snippet":  sqlStr,
				"type_string":   "sql",
				"is_external":   "true",
				"func_id":       string(ext.funcID),
			},
		}}); err != nil {
			return err
		}
		if callID != "" {

			if err := ext.emitEdgeKindLine(id, domain.CanonicalID(callID), domain.FactSummaryIO, line); err != nil {
				return err
			}
		}
	}

	if len(whereCols) > 0 {
		values := []ssa.Value{}
		for i := sqlArg + 1; i < len(cc.Args); i++ {
			values = append(values, variadicElems(cc.Args[i])...)
		}
		for i, arg := range values {
			if i >= len(whereCols) {
				break
			}
			col := sqlColUnqual(table, tableAlias, whereCols[i])
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
			realArg := arg
			if mi, ok := arg.(*ssa.MakeInterface); ok {
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

	for _, jp := range joinPairs {
		if jp.FromTable == "" || jp.FromCol == "" || jp.ToTable == "" || jp.ToCol == "" {
			continue
		}
		fromName := jp.FromTable + "." + jp.FromCol
		toName := jp.ToTable + "." + jp.ToCol
		fromID := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + fromName + ".read@" + fmt.Sprintf("%d", line))
		toID := domain.CanonicalID(string(ext.funcID) + "#ext.sql." + toName + ".filter@" + fmt.Sprintf("%d", line))
		fromProps := map[string]any{
			"full_path":     fromName,
			"instance_path": fromName,

			"access_kind":  "filter",
			"code_snippet": sqlStr,
			"type_string":  "sql",
			"is_external":  "true",
			"func_id":      string(ext.funcID),
			"origin":       "join",
		}
		toProps := map[string]any{
			"full_path":     toName,
			"instance_path": toName,
			"access_kind":   "filter",
			"code_snippet":  sqlStr,
			"type_string":   "sql",
			"is_external":   "true",
			"func_id":       string(ext.funcID),
			"origin":        "join",
		}
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID: fromID, Kind: domain.KindFieldAccess, Name: fromName,
			FilePath: ext.currentFile, LineStart: line, Properties: fromProps,
		}}); err != nil {
			return err
		}
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID: toID, Kind: domain.KindFieldAccess, Name: toName,
			FilePath: ext.currentFile, LineStart: line, Properties: toProps,
		}}); err != nil {
			return err
		}
		if err := ext.emitEdgeKindLine(fromID, toID, domain.FactDataFlowsTo, line); err != nil {
			return err
		}
	}
	return nil
}
