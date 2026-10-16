package ssa

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// TestLogDynInterfaceDispatchSkipsExpensiveFields：Debug 关闭（默认 Info
// 级）时不得构造 cc.String()/cc.Value.Type().String() 等昂贵字段。用 nil
// CallCommon 证明函数在构造前就 return（一旦求值即 panic）。
func TestLogDynInterfaceDispatchSkipsExpensiveFields(t *testing.T) {
	logger := zaptest.NewLogger(t, zaptest.Level(zap.InfoLevel))
	logDynInterfaceDispatch(logger, nil, 0) // 不 panic 即通过
}

// BenchmarkLogDynInterfaceDispatchDisabled：Debug 关闭路径的短基准——
// 只有一次 level 检查开销，不渲染符号字符串。
func BenchmarkLogDynInterfaceDispatchDisabled(b *testing.B) {
	logger := zap.NewNop()
	for i := 0; i < b.N; i++ {
		logDynInterfaceDispatch(logger, nil, 3)
	}
}
