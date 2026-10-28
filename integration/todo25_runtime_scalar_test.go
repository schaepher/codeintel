//go:build integration

package integration

// todo25 §13.3/§13.4：v1/v2 标量快照、静态常量、返回值相关性、重绑定。

import (
	"strings"
	"testing"
)

func TestTodo25RuntimeV1Scalar(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, snap := writeRuntimeRepo(t, 2, todo25RuntimeV1, "runtime.json")
	root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Run", 8, snap)
	assertRuntimeCounts(t, root, 1, 0, 1)
	ev := runtimeEvidence(root)
	if ev == nil {
		t.Fatal("缺少运行时快照证据")
	}
	if ev.Version != 1 || ev.SourceID != "review" || ev.Table != "review_config" ||
		ev.ScanTarget != "enabled" || ev.Result != "row" || ev.Rows != 1 {
		t.Errorf("快照证据不符: %+v", ev)
	}
	if len(ev.Query) != 2 || len(ev.Args) != 2 {
		t.Errorf("查询键/实参应从实际调用传播: %+v", ev)
	}
	if got := runtimeBranchStatus(root, "enabled == 1"); got != "taken" {
		t.Errorf("enabled == 1 应为 taken，got %q", got)
	}
	if got := runtimeBranchStatus(root, "err != nil"); got != "unknown" {
		t.Errorf("err != nil 应保留错误边界（unknown），got %q", got)
	}

	// 不带快照：保留未知分支；Static 不依赖快照选择 Manual。
	noSnap, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Run", 8, "")
	assertRuntimeCounts(t, noSnap, 1, 1, 1)
	static, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Static", 4, "")
	assertRuntimeCounts(t, static, 1, 0, 0)
	shadow, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:ConstShadow", 4, "")
	assertRuntimeCounts(t, shadow, 1, 0, 0)
}

func TestTodo25RuntimeV2ScalarAndScope(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, snap := writeRuntimeRepo(t, 2, todo25RuntimeV2, "runtime-v2.json")
	root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Run", 8, snap)
	assertRuntimeCounts(t, root, 1, 0, 1)
	ev := runtimeEvidence(root)
	if ev == nil || ev.Version != 2 || ev.Revision != "sample-42" {
		t.Errorf("v2 证据不符: %+v", ev)
	}

	// node 3：scope 内第二行 → Auto。
	dir3, snap3 := writeRuntimeRepo(t, 3, todo25RuntimeV2, "runtime-v2.json")
	root3, _ := runtimeSeqTree(t, dir3, "symbol:go:example.com/runtimeflow:Run", 8, snap3)
	assertRuntimeCounts(t, root3, 0, 1, 1)

	// node 4：不在 scope 内 → 不能说"快照没有就是完整空结果"，保留未知。
	dir4, snap4 := writeRuntimeRepo(t, 4, todo25RuntimeV2, "runtime-v2.json")
	root4, _ := runtimeSeqTree(t, dir4, "symbol:go:example.com/runtimeflow:Run", 8, snap4)
	assertRuntimeCounts(t, root4, 1, 1, 1)
	if ev := runtimeEvidence(root4); ev == nil || !strings.Contains(ev.Reason, "没有覆盖") {
		t.Errorf("scope 不覆盖应给明确 unknown 原因: %+v", ev)
	}

	// context 删除/任意标签不改变入参结果。
	noCtx := strings.Replace(todo25RuntimeV2, `  "context": {"environment": "test"},`+"\n", "", 1)
	dirC, snapC := writeRuntimeRepo(t, 2, noCtx, "runtime-v2.json")
	rootC, _ := runtimeSeqTree(t, dirC, "symbol:go:example.com/runtimeflow:Run", 8, snapC)
	assertRuntimeCounts(t, rootC, 1, 0, 1)
	ctxMY := strings.Replace(todo25RuntimeV2, `"environment": "test"`, `"environment": "prod", "region": "MY"`, 1)
	dirM, snapM := writeRuntimeRepo(t, 2, ctxMY, "runtime-v2.json")
	rootM, _ := runtimeSeqTree(t, dirM, "symbol:go:example.com/runtimeflow:Run", 8, snapM)
	assertRuntimeCounts(t, rootM, 1, 0, 1)
}

func TestTodo25RuntimeReturnCorrelation(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, snap := writeRuntimeRepo(t, 2, todo25RuntimeV1, "runtime.json")
	for _, tc := range []struct {
		target       string
		manual, auto int
		failure      int
	}{
		{"TwoReads", 1, 1, 1},
		{"Rebind", 1, 1, 0},
		{"RunChecked", 1, 0, 1},
	} {
		root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:"+tc.target, 10, snap)
		assertRuntimeCounts(t, root, tc.manual, tc.auto, tc.failure)
	}
}
