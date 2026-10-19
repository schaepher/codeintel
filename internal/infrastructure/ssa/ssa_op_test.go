package ssa

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

// TestSSAOpMatchesTypeName（todo8-7）：所有实际 SSA 值的操作名与旧
// `TrimPrefix(fmt.Sprintf("%T", v), "*ssa.")` 逐字一致；带类型的 nil 指针
// 与无类型 nil 同上。
func TestSSAOpMatchesTypeName(t *testing.T) {
	s, _ := testFuncSnapshotFilesAndProg(t, dispatchReuseFixture())
	for _, v := range fixtureValues(s) {
		want := strings.TrimPrefix(fmt.Sprintf("%T", v), "*ssa.")
		if got := ssaOp(v); got != want {
			t.Errorf("ssaOp(%T) = %q, want %q", v, got, want)
		}
	}
	var alloc *ssa.Alloc
	if got, want := ssaOp(alloc), "Alloc"; got != want {
		t.Errorf("typed nil = %q, want %q", got, want)
	}
	var untypedNil ssa.Value
	if got := ssaOp(untypedNil); got != "<nil>" {
		t.Errorf("untyped nil = %q, want <nil>", got)
	}
}
