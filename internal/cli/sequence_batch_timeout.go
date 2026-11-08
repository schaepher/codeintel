package cli

// B11e.3：manifest timeout 的存在性解码（缺省 vs 显式空值/null/类型错误）。

import "encoding/json"

// seqBatchTimeout 保留字段存在性：缺省（未出现）与显式 ""/null 不同。
// 不能用普通 string 解码——两者都会得到零值，导致非法 timeout 被当默认值。
type seqBatchTimeout struct {
	set bool
	raw json.RawMessage
}

// UnmarshalJSON 记录原始值（类型/合法性在校验阶段按 flow 报错）。
func (t *seqBatchTimeout) UnmarshalJSON(b []byte) error {
	t.set = true
	t.raw = append(t.raw[:0], b...)
	return nil
}

// value 显式字符串值（非字符串/null/类型错误 → ok=false）。
func (t seqBatchTimeout) value() (string, bool) {
	if !t.set {
		return "", false
	}
	var s string
	if err := json.Unmarshal(t.raw, &s); err != nil {
		return string(t.raw), false
	}
	return s, true
}
