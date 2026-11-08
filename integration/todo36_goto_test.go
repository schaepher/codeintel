//go:build integration

package integration

// todo36：U04bU.1 goto 标签目标资源可达性。

import "testing"

func u36Conn(t *testing.T, entry string, dir string) (int, bool) {
	root := u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg(entry))
	wf, _ := root["workflow"].(map[string]any)
	return u34ConnCount(root), u35Bool(wf["partial"])
}

// TestResourceGotoTargetWrites 正例：跳过改写保留连接；真实目标改写失效。
func TestResourceGotoTargetWrites(t *testing.T) {
	dir := u36Repo(t)
	t.Run("skip", func(t *testing.T) {
		n, partial := u36Conn(t, "ReviewGotoSkip", dir)
		if n != 1 || partial {
			t.Fatalf("ReviewGotoSkip = %d connections, partial=%v（want 1/false）", n, partial)
		}
	})
	t.Run("reset", func(t *testing.T) {
		n, partial := u36Conn(t, "ReviewGotoReset", dir)
		if n != 0 || partial {
			t.Fatalf("ReviewGotoReset = %d connections, partial=%v（want 0/false）", n, partial)
		}
	})
}

// TestResourceGotoInvocationIdentity 标签路径按函数/调用位置判断。
func TestResourceGotoInvocationIdentity(t *testing.T) {
	dir := u36Repo(t)
	t.Run("positive", func(t *testing.T) {
		for _, entry := range []string{"reviewFileGotoSkip", "reviewFileGotoOther",
			"reviewFileGotoAfter", "ReviewGotoAfterReset", "ReviewGotoOtherReset"} {
			n, _ := u36Conn(t, entry, dir)
			if n != 1 {
				t.Fatalf("%s connections = %d（want 1）", entry, n)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		n, _ := u36Conn(t, "reviewFileGotoReset", dir)
		if n != 0 {
			t.Fatalf("reviewFileGotoReset connections = %d（want 0）", n)
		}
	})
}

// TestResourceGotoBudget 有界路径完整；循环/超预算 fail-closed 且 partial。
func TestResourceGotoBudget(t *testing.T) {
	dir := u36Repo(t)
	t.Run("complete", func(t *testing.T) {
		n, partial := u36Conn(t, "ReviewGotoSkip", dir)
		if n != 1 || partial {
			t.Fatalf("ReviewGotoSkip = %d/%v（want 1/false）", n, partial)
		}
	})
	t.Run("exhausted", func(t *testing.T) {
		root := u35Query(t, dir, "Submit", "--related-entry", u35ReviewArg("ReviewGotoBudget"))
		if n := u34ConnCount(root); n != 0 {
			t.Fatalf("ReviewGotoBudget connections = %d（want 0）", n)
		}
		wf, _ := root["workflow"].(map[string]any)
		if !u35Bool(wf["partial"]) {
			t.Fatalf("ReviewGotoBudget 应标 partial: %v", wf["instance_boundaries"])
		}
	})
}
