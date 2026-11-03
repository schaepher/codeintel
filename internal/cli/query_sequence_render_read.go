package cli

// U04e：读取边界 Note 渲染（与 JSON 的 evidence.storage_read 同源）。

import "github.com/schaepher/codeintel/internal/action"

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
