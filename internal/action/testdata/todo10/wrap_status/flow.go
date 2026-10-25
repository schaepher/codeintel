// Package flow 包装函数改变返回状态（todo10-8 夹具 1）。
//
// 场景：请求带入默认状态（pending），包装函数调用一次模型/业务函数后把
// 状态改写为最终值才返回——用来验证"传入的默认状态"与"最终返回的状态"
// 在代码级时序里可区分（--focus return / --focus Resp.State）。
//
// 全部符号/数据中性，不依赖任何私有仓库或线上配置。
package flow

// Request 入参：带默认状态。
type Request struct {
	Region string
	State  string
}

// Resp 返回：最终状态。
type Resp struct {
	State string
	Body  string
}

// DefaultState 传入的默认状态（包装函数未改写时就是这个值）。
func DefaultState() string { return "pending" }

// decide 业务判定（中性：只按 region 前缀决定结果）。
func decide(req *Request) *Resp {
	return &Resp{State: "decided", Body: req.Region + ":ok"}
}

// audit 无关副作用（不属于返回状态路径——用于验证 focus 过滤）。
func audit(req *Request) {
	_ = req
}

// Wrap 包装：默认状态 → 业务调用 → 改写最终状态 → 返回。
func Wrap(req *Request) *Resp {
	req.State = DefaultState()
	out := decide(req)
	out.State = req.State
	audit(req)
	return out
}
