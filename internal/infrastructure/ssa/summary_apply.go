package ssa

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// applySummary 对带摘要的外部函数调用生成虚拟节点与边。
// 返回 false 表示无摘要（或无需处理）。
func (ext *fieldExtractor) applySummary(cc *ssa.CallCommon, callee *ssa.Function, callVal ssa.Value) (bool, error) {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).applySummary")
	defer logger.Debug("exit (fieldExtractor).applySummary")
	if len(ext.specs) == 0 {
		return false, nil
	}
	key := summaryKey(callee)
	spec, ok := ext.specs[key]
	if !ok {
		return false, nil
	}

	calleeID, ok := ext.funcIDOfFn(callee)
	if !ok {
		return false, nil
	}
	if !ext.extSummaries[calleeID] {
		ext.extSummaries[calleeID] = true
		specJSON := fmt.Sprintf(`{"reads":%q,"writes":%q,"param_index":%d}`,
			strings.Join(spec.Reads, ","), strings.Join(spec.Writes, ","), spec.ParamIndex)
		if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
			ID:   calleeID,
			Kind: domain.KindExternalSummary,
			Name: callee.Name(),
			Properties: map[string]any{
				"summary_json": specJSON,
				"func_id":      string(ext.funcID),
			},
		}}); err != nil {
			return true, err
		}
	}

	if spec.SQLStmt {
		return true, ext.applySQLSummary(cc, calleeID, spec, callVal, 1)
	}

	if spec.TxBoundary != "" {
		return true, ext.applyTxBoundary(cc, calleeID, spec.TxBoundary)
	}

	if spec.ORMWrite {
		return true, ext.applyORMWrite(cc, calleeID)
	}

	if spec.ORMRead {
		return true, ext.applyORMRead(cc, calleeID)
	}

	if spec.ScanOut {
		if err := ext.applyScanOut(cc, calleeID); err != nil {
			return true, err
		}
	}

	// kind 分派（Q177 修复）：XORM 静态 spec（xorm.io/xorm.(Session).X
	// 普通键——真实 *xorm.Session 具体类型调用）——与接口摘要共用
	// applySpecKind 的 table/filter/write/read/sql 逻辑
	if spec.Kind != "" {
		return ext.applySpecKind(cc, callVal, spec, key)
	}

	start := spec.ParamIndex
	if spec.ParamIndex < 0 || spec.ParamIndex >= len(cc.Args) {
		return true, nil
	}
	for i := start; i < len(cc.Args); i++ {
		arg := cc.Args[i]
		if err := ext.applyArgSummary(cc, calleeID, spec, i, arg); err != nil {
			return true, err
		}
	}
	return true, nil
}




// applyTxBoundary 事务边界（Q97）：Begin/Commit/Rollback → 事务虚拟节点
// （Name=sql.tx.<boundary>），标注事务边界位置。
func (ext *fieldExtractor) applyTxBoundary(cc *ssa.CallCommon, calleeID domain.CanonicalID, boundary string) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).applyTxBoundary")
	defer logger.Debug("exit (fieldExtractor).applyTxBoundary")
	line := ext.prog.Fset.PositionFor(cc.Pos(), false).Line
	name := "sql.tx." + boundary
	id := domain.CanonicalID(string(ext.funcID) + "#ext." + name + "@" + fmt.Sprintf("%d", line))
	return ext.emit(domain.Item{Node: &domain.CodeEntity{
		ID:        id,
		Kind:      domain.KindFieldAccess,
		Name:      name,
		FilePath:  ext.currentFile,
		LineStart: line,
		Properties: map[string]any{
			"full_path":     name,
			"instance_path": name,
			"access_kind":   "write",
			"type_string":   "tx",
			"is_external":   "true",
			"func_id":       string(ext.funcID),
		},
	}})
}

// gormTypeOf 提取 gorm:"type:xxx" 的字段类型（无则空——#243 表详情
// 自动初稿尽力而为）。
func gormTypeOf(tag reflect.StructTag) string {
	g := tag.Get("gorm")
	for _, part := range strings.Split(g, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "type:") {
			if t := strings.TrimSpace(strings.TrimPrefix(part, "type:")); t != "" {
				return t
			}
		}
	}
	return ""
}

// gormColumnOf 提取 gorm:"column:x" 的列名（无则 snake_case 字段名）。

func gormColumnOf(tag reflect.StructTag, fieldName string) string {
	g := tag.Get("gorm")
	for _, part := range strings.Split(g, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "column:") {
			if col := strings.TrimSpace(strings.TrimPrefix(part, "column:")); col != "" {
				return col
			}
		}
	}
	return snakeCase(fieldName)
}
