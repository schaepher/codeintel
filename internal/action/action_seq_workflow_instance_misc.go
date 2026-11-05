package action

// U04f：实例写入投影与预算辅助（从 action_seq_workflow_instance.go 拆出）。

import (
	"sort"
)

// seqWFInstanceWrites 写入 → JSON（按出现顺序）。
func seqWFInstanceWrites(ws []seqWFWrite) []SeqInstanceWrite {
	var out []SeqInstanceWrite
	for _, w := range ws {
		item := SeqInstanceWrite{WriteID: w.ID, Entry: w.Entry, File: w.File,
			Line: w.Line, Table: w.Table, Operation: w.Op, Guard: w.Guard}
		for _, v := range w.Values {
			item.Values = append(item.Values, SeqInstanceValue{Column: v.Column,
				Origin: v.Value.Origin, Kind: v.Value.Kind})
		}
		out = append(out, item)
	}
	return out
}

// seqWFWriteIDs 实例可达写入身份（循环判定用）。
func seqWFWriteIDs(ws []seqWFWrite) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.ID)
	}
	sort.Strings(out)
	return out
}

// flattenWFReadValues 合并多连接的读取值（去重）。
func flattenWFReadValues(conns []SeqWorkflowConnection, idxs []int) []SeqReadValue {
	var out []SeqReadValue
	seen := map[string]bool{}
	for _, i := range idxs {
		for _, v := range conns[i].ReadValues {
			key := v.ReadID + "|" + v.Column
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, v)
		}
	}
	return out
}

// appendMissing 追加缺失证明（去重）。
func appendMissing(base []string, extra ...string) []string {
	for _, e := range extra {
		base = appendUniqueStr(base, e)
	}
	return base
}

// parseWFNum 严格数值解析（用于 < <= > >=）。
func parseWFNum(s string) (int64, bool) {
	var n int64
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int64(s[i]-'0')
	}
	return n, true
}
