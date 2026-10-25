// Package flow 跨审核与定时任务推进（todo10-8 夹具 2）。
//
// 场景：人工审核入口（Approve）写状态；定时任务入口（Sweep）扫待审单
// 自动推进。两者经同一状态字段与同一张表关联——用来验证"异步生命周期
// 关联"（todo10-6）的证据判据：仅共享表不足以证明因果，需调用链/字段流
// 证据。
//
// 全部符号/数据中性。
package flow

// Order 待审单（状态字段是唯一的状态载体）。
type Order struct {
	ID    string
	State string
}

// Approve 人工审核入口：人工动作 → 写状态。
func Approve(o *Order) {
	o.State = "approved"
}

// Sweep 定时任务入口：扫待审集合 → 自动推进。
func Sweep(orders []*Order) {
	for _, o := range orders {
		if o.State == "pending" {
			o.State = "auto_done"
		}
	}
}

// Pending 待审集合（两个入口共享的数据来源）。
func Pending() []*Order { return nil }
