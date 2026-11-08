//go:build integration

package integration

// todo35：B06e.3/.4 + U04bU.1–4（源码 → init → CLI）。

import "testing"

// TestCommitAllNormalExits B06e.3：switch 提前返回 / Commit 失败吞掉不能证明。
func TestCommitAllNormalExits(t *testing.T) {
	dir := u35Repo(t)
	t.Run("positive", func(t *testing.T) {
		n, ok, missing := u34Commit(t, u35Query(t, dir, "SubmitTx", "--related-entry",
			u35ReviewArg("Review")), "SubmitTx", "Review")
		if n != 1 || !ok || missing {
			t.Fatalf("SubmitTx = %d/%v/%v（want 1/true/false）", n, ok, missing)
		}
	})
	t.Run("negative", func(t *testing.T) {
		for _, entry := range []string{"SubmitSwitchBypass", "SubmitSwallowCommit"} {
			n, ok, missing := u34Commit(t, u35Query(t, dir, entry, "--related-entry",
				u35ReviewArg("Review")), entry, "Review")
			if n != 1 || ok || !missing {
				t.Fatalf("%s = %d/%v/%v（want 1/false/true）", entry, n, ok, missing)
			}
		}
	})
}

// TestCommitLocalAndPackageReturns B06e.4：局部变量/命名返回值/跨文件/跨包 helper。
func TestCommitLocalAndPackageReturns(t *testing.T) {
	dir := u35Repo(t)
	t.Run("positive", func(t *testing.T) {
		for _, entry := range []string{"SubmitLocalHelperTx", "SubmitFileHelperTx",
			"SubmitNamedFileTx", "SubmitCrossPkgTx", "SubmitCrossPkgNamedTx"} {
			n, ok, missing := u34Commit(t, u35Query(t, dir, entry, "--related-entry",
				u35ReviewArg("Review")), entry, "Review")
			if n != 1 || !ok || missing {
				t.Fatalf("%s = %d/%v/%v（want 1/true/false）", entry, n, ok, missing)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		// 冲突返回（分支返回不同事务）与调用点重绑都不能证明原写入。
		for _, entry := range []string{"SubmitFileConflict", "SubmitHelperRebind"} {
			n, ok, _ := u34Commit(t, u35Query(t, dir, entry, "--related-entry",
				u35ReviewArg("Review")), entry, "Review")
			if ok {
				t.Fatalf("%s = %d/%v（want 无 commit_success）", entry, n, ok)
			}
		}
		// 跨包冲突返回：没有证明就不给 commit_success（连接是否可建立取决于
		// 事务资源身份；这里只断言证明不被借用）。
		if _, ok, _ := u34Commit(t, u35Query(t, dir, "SubmitConflictHelperTx",
			"--related-entry", u35ReviewArg("Review")), "SubmitConflictHelperTx", "Review"); ok {
			t.Fatal("SubmitConflictHelperTx 不应获得 commit_success")
		}
	})
}

// TestResourceReachableLoopWrites U04bU.1：break/continue/标签后的改写不可达。
func TestResourceReachableLoopWrites(t *testing.T) {
	dir := u35Repo(t)
	conns := func(entry string) int {
		return u34ConnCount(u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg(entry)))
	}
	t.Run("positive", func(t *testing.T) {
		for _, entry := range []string{"ReviewBreakBeforeReset", "ReviewContinueReset",
			"ReviewLabeledBreak", "ReviewLabeledContinue"} {
			if n := conns(entry); n != 1 {
				t.Fatalf("%s connections = %d（want 1）", entry, n)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		// 把改写移到 break 前，连接必须消失（同一事实改变即失效）。
		if n := conns("ReviewLoopOnce"); n != 0 {
			t.Fatalf("ReviewLoopOnce connections = %d（want 0）", n)
		}
	})
}

// TestResourcePanicTermination U04bU.2：内建 panic 终止；遮蔽后不终止。
func TestResourcePanicTermination(t *testing.T) {
	dir := u35Repo(t)
	conns := func(entry string) int {
		return u34ConnCount(u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg(entry)))
	}
	t.Run("positive", func(t *testing.T) {
		for _, entry := range []string{"ReviewPanicReset", "ReviewEarlyReset"} {
			if n := conns(entry); n != 1 {
				t.Fatalf("%s connections = %d（want 1）", entry, n)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		for _, entry := range []string{"ReviewShadowPanicReset", "ReviewPanicNoTerminate"} {
			if n := conns(entry); n != 0 {
				t.Fatalf("%s connections = %d（want 0）", entry, n)
			}
		}
	})
}

// TestResourcePointerParameterWrites U04bU.3：跨包指针参数写入原资源。
func TestResourcePointerParameterWrites(t *testing.T) {
	dir := u35Repo(t)
	conns := func(entry string) int {
		return u34ConnCount(u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg(entry)))
	}
	t.Run("positive", func(t *testing.T) {
		for _, entry := range []string{"ReviewPointerPeek", "ReviewPointerOtherReset",
			"ReviewPointerAfterReset"} {
			if n := conns(entry); n != 1 {
				t.Fatalf("%s connections = %d（want 1）", entry, n)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		if n := conns("ReviewPointerReset"); n != 0 {
			t.Fatalf("ReviewPointerReset connections = %d（want 0）", n)
		}
	})
}

// TestResourceBudgetFailClosed U04bU.4：预算内可证明；超限 fail-closed 且原因可见。
func TestResourceBudgetFailClosed(t *testing.T) {
	dir := u35Repo(t)
	t.Run("positive", func(t *testing.T) {
		// 完整访问的包装函数（无改写）可连接；浅链改写能被访问到。
		root := u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg("ReviewSafeChain"))
		if n := u34ConnCount(root); n != 1 {
			t.Fatalf("ReviewSafeChain connections = %d（want 1）", n)
		}
		wf, _ := root["workflow"].(map[string]any)
		if wf != nil && u35Bool(wf["partial"]) {
			t.Fatalf("ReviewSafeChain 不应标 partial: %v", wf["instance_boundaries"])
		}
		root = u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg("ReviewShallowReset"))
		if n := u34ConnCount(root); n != 0 {
			t.Fatalf("ReviewShallowReset connections = %d（want 0）", n)
		}
		wf, _ = root["workflow"].(map[string]any)
		if wf != nil && u35Bool(wf["partial"]) {
			t.Fatalf("ReviewShallowReset 预算内不应残留 partial 原因: %v", wf["instance_boundaries"])
		}
		// 公开示例的 11 层链也必须访问到真实改写。
		root = u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg("ReviewDeepReset"))
		if n := u34ConnCount(root); n != 0 {
			t.Fatalf("ReviewDeepReset connections = %d（want 0）", n)
		}
	})
	t.Run("negative", func(t *testing.T) {
		root := u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg("ReviewBudgetReset"))
		if n := u34ConnCount(root); n != 0 {
			t.Fatalf("ReviewBudgetReset connections = %d（want 0，预算耗尽 fail-closed）", n)
		}
		wf, _ := root["workflow"].(map[string]any)
		if wf == nil || !u35Bool(wf["partial"]) {
			t.Fatalf("ReviewBudgetReset 应标 partial: %v", wf)
		}
		got := false
		for _, v := range asAnySlice(wf["instance_boundaries"]) {
			if asString(v) == "resource_analysis_budget" {
				got = true
			}
		}
		if !got {
			t.Fatalf("预算耗尽原因缺失: %v", wf["instance_boundaries"])
		}
	})
}
