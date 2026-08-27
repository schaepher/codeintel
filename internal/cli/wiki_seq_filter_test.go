package cli

// R100 待办7：时序图 filter 扩展 wiki 入口——filter 现只在 query
// sequence --code；wiki grpc 方法时序图同源消费 CodeSequence，filter
// 经 wikiRenderCtx.SeqFilter 传入后过滤生效。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSeqFilter 覆盖 agentConfigPath 指向临时配置（写 seq.filter_fns）。
func withSeqFilter(t *testing.T, fns []string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	var b strings.Builder
	b.WriteString("seq:\n  filter_fns:\n")
	for _, f := range fns {
		b.WriteString("    - " + f + "\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	old := agentConfigPath
	agentConfigPath = func() string { return p }
	t.Cleanup(func() { agentConfigPath = old })
}

// TestWikiProcSeqFilter：filter_fns 命中 → wiki 代码级时序过滤生效
// （同 query sequence --code 行为——解析端命中不生成节点）。
func TestWikiProcSeqFilter(t *testing.T) {
	dir := seedNestedSeqRepo(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	rc := &wikiRenderCtx{acts: acts, RepoAbs: dir, SeqDepth: 3,
		SeqStopPkgs: loadSeqStopPkgs(), Diagram: "mermaid"}
	md := renderProcSeqMD(rc, "Prepare", nil, "")
	if !strings.Contains(md, "helper") {
		t.Fatalf("无 filter 应含 helper:\n%s", md)
	}
	// filter_fns 命中 helper → 时序图不含 helper（LoadItems 保留）
	withSeqFilter(t, []string{"helper"})
	rc2 := &wikiRenderCtx{acts: acts, RepoAbs: dir, SeqDepth: 3,
		SeqStopPkgs: loadSeqStopPkgs(), SeqFilter: loadSeqFilter(), Diagram: "mermaid"}
	md2 := renderProcSeqMD(rc2, "Prepare", nil, "")
	if strings.Contains(md2, "helper") {
		t.Errorf("filter_fns 应过滤 helper:\n%s", md2)
	}
	if !strings.Contains(md2, "LoadItems") {
		t.Errorf("未命中过滤的调用应保留:\n%s", md2)
	}
	html := renderProcSeqHTML(rc2, "Prepare", nil, "")
	if strings.Contains(html, "helper") {
		t.Errorf("html 版 filter 应过滤 helper:\n%s", html)
	}
}
