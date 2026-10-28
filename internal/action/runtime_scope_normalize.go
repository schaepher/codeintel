package action

// U01：运行时 scope 的归一化与身份键（IN 值去重 + 稳定排序判重）。

import (
	"fmt"
	"sort"
	"strings"
)

// scopeDisplay 归一化 scope 的稳定文本（错误消息/证据用）：
// `node_type in [2,3]`、`region = ID`（多谓词按字段序以 && 连接）。
func (o *runtimeObservation) scopeDisplay() string {
	parts := make([]string, 0, len(o.Scope))
	for _, p := range o.Scope {
		if p.Op == "in" && p.Value.kind == rtSlice {
			parts = append(parts, p.Field+" in "+p.Value.rtDisplay())
			continue
		}
		parts = append(parts, p.Field+" = "+p.Value.rtDisplay())
	}
	if len(parts) == 0 {
		return "scope=[]"
	}
	return strings.Join(parts, " && ")
}

func (o *runtimeObservation) identityKey() string {
	return o.SourceID + "\x00" + o.Table + "\x00" + runtimeScopeKey(o.Scope)
}

func runtimeScopeKey(scope []runtimePredicate) string {
	parts := make([]string, 0, len(scope))
	for _, p := range scope {
		// 值键带类型标记：int 2 与 string "2" 是不同 scope（保留原有类型区别）。
		parts = append(parts, p.Field+"\x00"+p.Op+"\x00"+runtimeValueKey(p.Value))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

// runtimeValueKey 值身份键（仅供 scope 判重/排序；不参与业务比较）。
// IN 列表按元素身份去重后排序，`[2,3]` 与 `[3,2,2]` 归一化后相同。
func runtimeValueKey(v rtValue) string {
	if v.kind == rtSlice {
		seen := map[string]bool{}
		keys := make([]string, 0, len(v.elems))
		for _, e := range v.elems {
			k := runtimeScalarKey(e)
			if seen[k] {
				continue
			}
			seen[k] = true
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return "in[" + strings.Join(keys, ",") + "]"
	}
	return runtimeScalarKey(v)
}

func runtimeScalarKey(v rtValue) string {
	switch v.kind {
	case rtInt:
		return "i:" + v.rtDisplay()
	case rtFloat:
		return "f:" + v.rtDisplay()
	case rtString:
		return "s:" + v.s
	case rtBool:
		return "b:" + v.rtDisplay()
	case rtNull:
		return "null"
	default:
		return fmt.Sprintf("%d:%s", v.kind, v.rtDisplay())
	}
}

// normalizeRuntimeScope IN 谓词值去重并按稳定值语义排序（U01）。
func normalizeRuntimeScope(scope []runtimePredicate) {
	for i := range scope {
		if scope[i].Op != "in" || scope[i].Value.kind != rtSlice {
			continue
		}
		elems := make([]rtValue, 0, len(scope[i].Value.elems))
		seen := map[string]bool{}
		for _, e := range scope[i].Value.elems {
			k := runtimeScalarKey(e)
			if seen[k] {
				continue
			}
			seen[k] = true
			elems = append(elems, e)
		}
		sort.SliceStable(elems, func(a, b int) bool {
			return runtimeScalarKey(elems[a]) < runtimeScalarKey(elems[b])
		})
		scope[i].Value = rtSliceValue(elems)
	}
}
