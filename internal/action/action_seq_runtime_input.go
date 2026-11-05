package action

// T14b：RuntimeData 兼容入口归一（从 action_seq_types.go 拆出）。

import "fmt"

// runtimeDataInput RuntimeData 兼容入口归一（T14b）：同时接受路径字符串与
// 已解析快照。typed-nil 等同未配置；其它类型明确失败。
func runtimeDataInput(v any) (string, *RuntimeSnapshot, error) {
	switch x := v.(type) {
	case nil:
		return "", nil, nil
	case string:
		return x, nil, nil
	case *RuntimeSnapshot:
		if x == nil {
			return "", nil, nil
		}
		return "", x, nil
	}
	return "", nil, fmt.Errorf("runtime-data: RuntimeData 类型不支持（%T）——只接受路径字符串或 LoadRuntimeData 返回的快照", v)
}

// runtimeDataPath 合并 RuntimeDataPath/RuntimeData 两个兼容入口（T14）：
// 都没有 → 空；同时给出且不一致 → 空（由 LoadRuntimeSnapshot 报错语义，
// 调用方在 CLI 层已做参数校验）。
func (r CodeSequenceRequest) runtimeDataPath() string {
	if r.RuntimeDataPath != "" {
		return r.RuntimeDataPath
	}
	if p, _, err := runtimeDataInput(r.RuntimeData); err == nil {
		return p
	}
	return ""
}

// hasRuntimeConfig 请求是否配置了运行时快照（任一种入口；typed-nil 不算）。
func (r CodeSequenceRequest) hasRuntimeConfig() bool {
	if r.RuntimeDataPath != "" || r.Runtime != nil {
		return true
	}
	p, snap, err := runtimeDataInput(r.RuntimeData)
	return err == nil && (p != "" || snap != nil)
}
