package cli

// U04e：读取边界 Note 渲染（与 JSON 的 evidence.storage_read 同源）。

import (
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// seqStorageReadNote 读取边界 Note（U04e；JSON 与 Mermaid 同源）。
func seqStorageReadNote(n *action.CodeSeqNode) string {
	if n == nil || n.Evidence == nil || n.Evidence.StorageRead == nil {
		return ""
	}
	debug := action.SeqStorageReadDebugText(n.Evidence.StorageRead)
	if debug == "" {
		return ""
	}
	return "读取边界：" + debug + "（读取结果与记录身份未证明）"
}

// seqORMNote XORM 操作边界 Note（U04f；JSON/Mermaid 同一份 evidence.orm）。
func seqORMNote(n *action.CodeSeqNode) string {
	if n == nil || n.Evidence == nil || n.Evidence.ORM == nil {
		return ""
	}
	orm := n.Evidence.ORM
	head := "ORM边界：" + orm.Framework
	if orm.Operation != "" {
		head += " " + orm.Operation
	}
	if orm.Table != "" {
		head += " " + orm.Table
	} else if orm.TableExpression != "" {
		head += " " + orm.TableExpression
	}
	detail := orm.Boundary
	if detail == "" {
		detail = action.SeqORMStateUnproven
	}
	if orm.PredicateCoverage != "" {
		detail += "；谓词覆盖率 " + orm.PredicateCoverage
	}
	note := head + "（" + detail + "；模型映射/生成键/写入成功未证明）"
	return strings.TrimSpace(note)
}
