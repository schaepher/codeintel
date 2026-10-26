package ssa

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap/zapcore"
)

func TestFuncDataStatsCounts(t *testing.T) {
	fd := map[domain.CanonicalID]*funcData{
		"a": {directReads: []fieldEntry{{fieldPath: "A.B"}}, directWrites: []fieldEntry{{}, {}},
			calls:          []callInfo{{calleeID: "x", argStructPaths: []string{"A"}, argNames: []string{"v", "w"}}},
			indirectWrites: []fieldEntry{{}}},
		"b": nil, // 空数据不应计入（防御 nil）
		"c": {calls: []callInfo{{calleeID: "y"}}},
	}
	fields := funcDataStats(fd)
	got := map[string]int{}
	for _, f := range fields {
		if f.Type == zapcore.Int64Type || f.Type == zapcore.Int32Type {
			got[f.Key] = int(f.Integer)
		}
	}
	want := map[string]int{"funcs": 3, "calls": 2, "arg_paths": 1, "arg_names": 2, "direct": 3, "indirect": 1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	// funcs 计的是 map 长度（含无摘要数据的条目），便于对照 totalFuncs
	if got["funcs"] != len(fd) {
		t.Errorf("funcs = %d, want %d", got["funcs"], len(fd))
	}
}
