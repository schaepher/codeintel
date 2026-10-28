//go:build integration

package integration

// todo25 §8：第 8 节完整脱敏夹具的逐项断言（真实 init → CLI）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func TestTodo25CoreFixtureAssertions(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := writeFlowFixture(t)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	sym := "symbol:go:example.com/flow:Entry"

	a := seqTreeJSON(t, dir, sym, 24, "--scene", "Source=7")
	for i := 1; i <= 4; i++ {
		if countSeqSym(a, ":Check"+itoaTest(i)) != 1 {
			t.Errorf("Source=7 Check%d 应一次", i)
		}
	}
	for i := 5; i <= 7; i++ {
		if countSeqSym(a, ":Check"+itoaTest(i)) != 0 {
			t.Errorf("Source=7 Check%d 不应执行", i)
		}
	}
	b := seqTreeJSON(t, dir, sym, 24, "--scene", "Source=19")
	for i := 1; i <= 7; i++ {
		if countSeqSym(b, ":Check"+itoaTest(i)) != 1 {
			t.Errorf("Source=19 Check%d 应一次", i)
		}
	}
	// 工厂副作用 Setup 在 Execute 前恰一次；First 返回闭包不提前执行。
	if countSeqSym(b, ":Setup") != 1 {
		t.Errorf("Setup 应恰一次，got %d", countSeqSym(b, ":Setup"))
	}
	if countSeqSym(b, ":MakeFirst") == 0 || countSeqSym(b, ":First") == 0 ||
		countSeqSym(b, ":Second") == 0 {
		t.Errorf("装饰器链缺 MakeFirst/First/Second")
	}
	// 事务回调内的 Store.Insert / ExecContext 与 ApproveTrace 顺序。
	if countSeqSym(b, ":(Store).Insert") == 0 {
		t.Errorf("存储链未展开")
	}
	var execCtx int
	walkSeq(b, func(n *action.CodeSeqNode) {
		if strings.Contains(n.Label, "ExecContext") {
			execCtx++
		}
	})
	if execCtx == 0 {
		t.Errorf("存储链未到 ExecContext")
	}
	if countSeqSym(b, ":ApproveTrace") == 0 {
		t.Errorf("正常返回后应有 ApproveTrace")
	}
	// bypass 两条分支都保留（Second 返回原回调 / 返回闭包）。
	var beforeSecond, afterSecond int
	walkSeq(b, func(n *action.CodeSeqNode) {
		switch n.Label {
		case "BeforeSecond":
			beforeSecond++
		case "AfterSecond":
			afterSecond++
		}
	})
	if beforeSecond == 0 || afterSecond == 0 {
		t.Errorf("bypass/非 bypass 两条路径都应保留: BeforeSecond=%d AfterSecond=%d",
			beforeSecond, afterSecond)
	}
	// IsRegionX 是运行时事实 → 条件分支保留（不按函数名/返回值猜地区）。
	var regionCond bool
	walkSeq(a, func(n *action.CodeSeqNode) {
		if strings.Contains(n.Label, "IsRegionX") {
			regionCond = true
		}
	})
	if !regionCond {
		t.Errorf("Source=7 应保留 IsRegionX 条件分支")
	}
	// Insert 的错误守卫保留；ApproveTrace 只在 Execute 成功后出现一次。
	if countSeqSym(b, ":ApproveTrace") != 1 {
		t.Errorf("ApproveTrace 应恰一次，got %d", countSeqSym(b, ":ApproveTrace"))
	}
	// 错误路径后继的精确位置：`if err != nil { return err }` 分支内不得含
	// ApproveTrace；ApproveTrace 必须是该分支之后的下一条顶层步骤（不是
	// 仅"整棵树里出现一次"）。
	var errBranch *action.CodeSeqNode
	errIdx, approveIdx := -1, -1
	for i, n := range b.Nodes {
		if n.Kind == "branch" && strings.Contains(n.Label, "err != nil") {
			errBranch, errIdx = n, i
		}
		if strings.Contains(n.Label, "ApproveTrace") {
			approveIdx = i
		}
	}
	if errBranch == nil {
		t.Fatalf("应保留 err != nil 错误守卫分支: %+v", b.Nodes)
	}
	walkSeq(errBranch, func(n *action.CodeSeqNode) {
		if strings.Contains(n.Label, "ApproveTrace") {
			t.Errorf("错误分支内不得出现 ApproveTrace: %+v", n)
		}
	})
	if approveIdx < 0 || errIdx < 0 || approveIdx <= errIdx {
		t.Errorf("ApproveTrace 应在 err != nil 分支之后（approve=%d err=%d）", approveIdx, errIdx)
	}
	for _, n := range b.Nodes[approveIdx+1:] {
		if n.Kind != "return" {
			t.Errorf("ApproveTrace 之后只应有 return（错误路径已提前返回），got %s|%s", n.Kind, n.Label)
		}
	}
}
