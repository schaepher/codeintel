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

// seqWriteErrorNote 写入错误槽 Note（U04h；JSON/Mermaid 同一份 storage）。
func seqWriteErrorNote(n *action.CodeSeqNode) string {
	if n == nil || n.Evidence == nil || n.Evidence.Storage == nil ||
		n.Evidence.Storage.ErrorResult == nil {
		return ""
	}
	res := n.Evidence.Storage.ErrorResult
	return "写入错误结果 " + res.CallID + "[" + itoaSeqInt(res.Slot) + "]（outcome=unproven）"
}

// seqWriteConditionNote 分支条件引用写入错误槽（U04h）。
func seqWriteConditionNote(n *action.CodeSeqNode) string {
	if n == nil || n.WriteCondition == nil || n.WriteCondition.Result == nil {
		return ""
	}
	c := n.WriteCondition
	return "写入错误结果 " + c.Result.CallID + "[" + itoaSeqInt(c.Result.Slot) + "]：" +
		"then=" + c.WhenTrue + "；else=" + c.WhenFalse + "（事务提交与影响行数未证明）"
}

func itoaSeqInt(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// seqEvidenceNotes 调用节点上的结构化证据注记（U04e/U04f/U04h 同源）。
func seqEvidenceNotes(n *action.CodeSeqNode) []string {
	var out []string
	if note := seqStorageReadNote(n); note != "" {
		out = append(out, note)
	}
	if note := seqORMNote(n); note != "" {
		out = append(out, note)
	}
	if note := seqWriteErrorNote(n); note != "" {
		out = append(out, note)
	}
	if note := seqRowCountNote(n); note != "" {
		out = append(out, note)
	}
	return out
}

// seqRowCountNote 影响行数槽 Note（U04j；与 JSON 同一分析结果）。
func seqRowCountNote(n *action.CodeSeqNode) string {
	if n == nil || n.Evidence == nil || n.Evidence.ORM == nil ||
		n.Evidence.ORM.RowCountResult == nil || n.Evidence.ORM.RowCountResult.Write == nil {
		return ""
	}
	rc := n.Evidence.ORM.RowCountResult
	return "写入影响行数 " + rc.Write.CallID + "[" + itoaSeqInt(rc.Slot) + "]（错误检查与事务提交未证明）"
}

// seqRowCountConditionNote 行数比较条件 Note（U04j）。
func seqRowCountConditionNote(n *action.CodeSeqNode) string {
	if n == nil || n.RowCountCondition == nil || n.RowCountCondition.Result == nil ||
		n.RowCountCondition.Result.Write == nil {
		return ""
	}
	c := n.RowCountCondition
	return "写入影响行数 " + c.Result.Write.CallID + "[" + itoaSeqInt(c.Result.Slot) + "]：" +
		"then=" + c.WhenTrue + "；else=" + c.WhenFalse + "（错误检查与事务提交未证明）"
}
