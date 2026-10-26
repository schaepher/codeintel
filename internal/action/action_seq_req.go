package action

import "context"

// focusActive 是否启用焦点过滤。
func (r CodeSequenceRequest) focusActive() bool {
	return r.focus != nil && (r.focus.returns || len(r.focus.fields) > 0)
}

// ctx 取消上下文（nil 安全）。
func (r CodeSequenceRequest) ctx() context.Context {
	if r.Ctx != nil {
		return r.Ctx
	}
	return context.Background()
}
