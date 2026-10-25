// Package flow 人工与内部自动推进并存（todo10-8 夹具 3）。
//
// 场景：同一流程节点既能被人工动作推进（AdvanceByHuman），也能被内部
// 审核调用推进（AdvanceBySystem）。用来验证证据里能区分"人工动作 /
// 内部调用 / 自动节点"，且不推断固定操作次数。
//
// 全部符号/数据中性。
package flow

// Step 流程节点。
type Step struct {
	Name  string
	Count int
}

// AdvanceByHuman 人工推进（入口：外部事件）。
func AdvanceByHuman(s *Step) {
	s.Count++
}

// AdvanceBySystem 内部自动推进（由内部审核流程调用）。
func AdvanceBySystem(s *Step) {
	s.Count++
}

// Review 内部审核流程：调用自动推进。
func Review(s *Step) {
	AdvanceBySystem(s)
}
