package ssa

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/tools/go/ssa"
)

// TestLogDynInterfaceDispatchSkipsExpensiveFields：Debug 关闭（默认 Info
// 级）时不得构造 cc.String()/cc.Value.Type().String() 等昂贵字段。用 nil
// CallCommon 证明函数在构造前就 return（一旦求值即 panic）。
func TestLogDynInterfaceDispatchSkipsExpensiveFields(t *testing.T) {
	logger := zaptest.NewLogger(t, zaptest.Level(zap.InfoLevel))
	logDynInterfaceDispatch(logger, nil, 0) // 不 panic 即通过
}

// firstCallCommon 从 fixture 取一个真实调用点（enabled 路径字段一致性用）。
func firstCallCommon(t *testing.T) *ssa.CallCommon {
	t.Helper()
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod":  baseModule,
		"main.go": "package main\n\nfunc helper(x int) int { return x + 1 }\n\nfunc foo() { _ = helper(1) }\n\nfunc main() { foo() }\n",
	})
	for _, fn := range s.all() {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				if c, ok := instr.(*ssa.Call); ok {
					return &c.Call
				}
			}
		}
	}
	t.Fatal("fixture 未找到调用点")
	return nil
}

// TestLogDynInterfaceDispatchEnabledFields：Debug 开启时字段内容与旧实现
// 逐字一致（impls/call/iface）；Info 级不产出。
func TestLogDynInterfaceDispatchEnabledFields(t *testing.T) {
	cc := firstCallCommon(t)

	core, logs := observer.New(zap.DebugLevel)
	logDynInterfaceDispatch(zap.New(core), cc, 5)
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("应输出 1 条日志, got %d", len(entries))
	}
	e := entries[0]
	if e.Message != "dyn interface dispatch" {
		t.Errorf("消息 = %q", e.Message)
	}
	ctx := e.ContextMap()
	if got := ctx["impls"]; got != int64(5) {
		t.Errorf("impls = %v (%T), want 5", got, got)
	}
	if got := ctx["call"]; got != cc.String() {
		t.Errorf("call = %v, want %q", got, cc.String())
	}
	if got := ctx["iface"]; got != cc.Value.Type().String() {
		t.Errorf("iface = %v, want %q", got, cc.Value.Type().String())
	}

	// Info 级（Debug 关闭）→ 不产出（且不构造字段）
	coreOff, logsOff := observer.New(zap.InfoLevel)
	logDynInterfaceDispatch(zap.New(coreOff), cc, 5)
	if logsOff.Len() != 0 {
		t.Errorf("Info 级不应输出 debug 日志, got %d", logsOff.Len())
	}
}

// BenchmarkLogDynInterfaceDispatchDisabled：Debug 关闭路径的短基准——
// 只有一次 level 检查开销，不渲染符号字符串。
func BenchmarkLogDynInterfaceDispatchDisabled(b *testing.B) {
	logger := zap.NewNop()
	for i := 0; i < b.N; i++ {
		logDynInterfaceDispatch(logger, nil, 3)
	}
}
