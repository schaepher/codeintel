package ast

// R92 测试：SourceHash 确定性 + 覆盖生产源码（embed 编译时快照）。

import "testing"

// TestSourceHashDeterministic：哈希非空且两次一致（embed 稳定）。
func TestSourceHashDeterministic(t *testing.T) {
	h1 := SourceHash()
	h2 := SourceHash()
	if h1 == "" || h1 == "unknown" {
		t.Fatalf("SourceHash = %q; want 非空", h1)
	}
	if h1 != h2 {
		t.Errorf("SourceHash 应确定: %q vs %q", h1, h2)
	}
}
