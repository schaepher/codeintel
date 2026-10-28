// U02：符号状态预算不得静默截断；已开始执行后的 context 取消必须停下。
package action

import (
	"context"
	"go/ast"
	"strings"
	"testing"
	"time"
)

// 超过预算 → 明确错误，且不丢尾部状态（不据此继续排除分支）。
func TestRuntimeStateBudgetNotSilentlyTruncated(t *testing.T) {
	e := &rtEval{maxStates: 4}
	in := make([]*rtState, 0, 8)
	for i := 0; i < 8; i++ {
		in = append(in, &rtState{env: map[*ast.Object]rtValue{}, types: map[*ast.Object]*runtimeType{}})
	}
	out := e.capStates(in)
	if e.err == nil {
		t.Fatal("超出状态预算必须报错，而不是静默截断")
	}
	want := "runtime-data: analysis state budget exhausted (4)"
	if e.err.Error() != want {
		t.Fatalf("err = %q, want %q", e.err.Error(), want)
	}
	if len(out) != len(in) {
		t.Fatalf("状态被截断: %d → %d", len(in), len(out))
	}
}

// 未超预算不报错（正常路径保持原状）。
func TestRuntimeStateBudgetWithinLimit(t *testing.T) {
	e := &rtEval{maxStates: 4}
	in := []*rtState{{env: map[*ast.Object]rtValue{}}, {env: map[*ast.Object]rtValue{}}}
	out := e.capStates(in)
	if e.err != nil {
		t.Fatalf("unexpected err: %v", e.err)
	}
	if len(out) != 2 {
		t.Fatalf("out = %d", len(out))
	}
}

// 已开始执行后的 ctx 取消：execBlock 停下并记 ErrSeqCanceled，不返回半棵树状态。
func TestRuntimeEvalCancelStopsMidExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := &rtEval{ctx: ctx, maxStates: 64}
	st := &rtState{env: map[*ast.Object]rtValue{}, types: map[*ast.Object]*runtimeType{}}
	out := e.execBlock(nil, []ast.Stmt{&ast.ExprStmt{X: &ast.Ident{Name: "x"}}}, []*rtState{st}, 0)
	if out != nil {
		t.Fatalf("取消后不得返回状态: %v", out)
	}
	if e.err != ErrSeqCanceled {
		t.Fatalf("err = %v, want ErrSeqCanceled", e.err)
	}
}

// flipCtx 显式控制取消时点（模拟"已开始执行后取消"）。
// 计时无法确定性复现中途中止，用可控的实现锁住"中途检查"语义。
type flipCtx struct {
	armed bool
}

func (c *flipCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *flipCtx) Done() <-chan struct{}       { return nil }
func (c *flipCtx) Value(any) any               { return nil }
func (c *flipCtx) Err() error {
	if c.armed {
		return context.Canceled
	}
	return nil
}

// 已开始执行后再取消：第一次执行正常，第二次停下且不产出状态。
func TestRuntimeEvalCancelAfterStart(t *testing.T) {
	ctx := &flipCtx{}
	e := &rtEval{ctx: ctx, maxStates: 64}
	st := &rtState{env: map[*ast.Object]rtValue{}, types: map[*ast.Object]*runtimeType{}}
	block := []ast.Stmt{&ast.ExprStmt{X: &ast.Ident{Name: "x"}}}
	if out := e.execBlock(nil, block, []*rtState{st}, 0); out == nil {
		t.Fatal("首次执行不应被取消")
	}
	ctx.armed = true
	if out := e.execBlock(nil, block, []*rtState{st}, 0); out != nil {
		t.Fatalf("取消后不得返回状态: %v", out)
	}
	if e.err != ErrSeqCanceled {
		t.Fatalf("err = %v, want ErrSeqCanceled", e.err)
	}
}

// 预算错误文本必须能定位到原因（CLI stderr 直出）。
func TestRuntimeStateBudgetErrorMentionsBudget(t *testing.T) {
	e := &rtEval{maxStates: 1}
	e.capStates([]*rtState{{env: map[*ast.Object]rtValue{}}, {env: map[*ast.Object]rtValue{}}})
	if e.err == nil || !strings.Contains(e.err.Error(), "budget") {
		t.Fatalf("err = %v, want budget mention", e.err)
	}
}
