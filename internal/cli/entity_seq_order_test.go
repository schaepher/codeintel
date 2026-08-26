package cli

// R13 时序顺序测试：调用链按源码调用行号排序（入口序 + 内部行号）——
// 验证"顺序与实际代码一一对应"（用户要求 1）。

import (
	"github.com/schaepher/codeintel/internal/action"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestSortChainByCallLine：深度 1 按行号、深度 2 按被调者在入口中的
// 位置 + 内部行号——还原源码序。
func TestSortChainByCallLine(t *testing.T) {
	entry := domain.CanonicalID("symbol:go:example.com/m:cmdServe")
	facts := []*domain.Fact{
		// 深度 2（被调者内部）：(Actions).Counts 内部调用 Reader
		{SourceID: "symbol:go:example.com/m:(Actions).Counts", TargetID: "symbol:go:example.com/m:(Reader).Count",
			Kind: domain.FactCalls, Metadata: map[string]any{"line_num": 3}},
		// 深度 1：乱序插入（SQL 遍历序）
		{SourceID: entry, TargetID: "symbol:go:example.com/m:(Actions).Latest", Kind: domain.FactCalls, Metadata: map[string]any{"line_num": 56}},
		{SourceID: entry, TargetID: "symbol:go:example.com/m:Open", Kind: domain.FactCalls, Metadata: map[string]any{"line_num": 43}},
		{SourceID: entry, TargetID: "symbol:go:example.com/m:(Actions).Counts", Kind: domain.FactCalls, Metadata: map[string]any{"line_num": 52}},
		{SourceID: "symbol:go:example.com/m:(Actions).Counts", TargetID: "symbol:go:example.com/m:(Reader).Latest",
			Kind: domain.FactCalls, Metadata: map[string]any{"line_num": 5}},
		// 深度 2（被调者内部）：server.New 内部调用
		{SourceID: "symbol:go:example.com/m:serverNew", TargetID: "symbol:go:example.com/m:handler",
			Kind: domain.FactCalls, Metadata: map[string]any{"line_num": 7}},
		{SourceID: entry, TargetID: "symbol:go:example.com/m:serverNew", Kind: domain.FactCalls, Metadata: map[string]any{"line_num": 73}},
		// 无行号边（fallback 排最后）
		{SourceID: entry, TargetID: "symbol:go:example.com/m:NoLine", Kind: domain.FactCalls},
	}
	steps := action.SortChainByCallLine(string(entry), facts)
	var got []string
	for _, s := range steps {
		got = append(got, s.Caller+"->"+s.Callee)
	}
	want := []string{
		"cmdServe->Open",                    // 43
		"cmdServe->(Actions).Counts",        // 52
		"(Actions).Counts->(Reader).Count",  // Counts 内部 3
		"(Actions).Counts->(Reader).Latest", // Counts 内部 5
		"cmdServe->(Actions).Latest",        // 56
		"cmdServe->serverNew",               // 73
		"serverNew->handler",                // serverNew 内部 7
		"cmdServe->NoLine",                  // 无行号 fallback 最后
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("顺序 = %v\nwant %v", got, want)
	}
}
