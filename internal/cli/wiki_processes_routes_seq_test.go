package cli

import (
	"strings"
	"testing"
)

// TestHTTPRouteEntryCodeSeq：#23——HTTP 路由入口应与 gRPC 入口同款走
// **代码级时序**（handler_id 可解析时为源码 AST 时序），而不是仅索引调用链；
// 无 handler_id（匿名/方法值）时 fallback 调用链。R100 待办7 的 filter 也
// 经同一 renderProcSeq 消费。
func TestHTTPRouteEntryCodeSeq(t *testing.T) {
	dir := seedNestedSeqRepo(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	rc := &wikiRenderCtx{acts: acts, RepoAbs: dir, SeqDepth: 3,
		SeqStopPkgs: loadSeqStopPkgs(), Diagram: "mermaid"}
	entries := []httpProcEntry{{
		Handler:   "Prepare",
		HandlerID: "symbol:go:example.com/m:Prepare",
		Paths:     []string{"GET /prepare"},
	}}
	md := renderHTTPRoutesMD(rc, entries, 10)
	if !strings.Contains(md, "**代码级时序**（源码 AST") {
		t.Errorf("HTTP 入口应走代码级时序块:\n%s", md)
	}
	if !strings.Contains(md, "LoadItems") {
		t.Errorf("代码级时序应含被调 LoadItems:\n%s", md)
	}
	if html := renderHTTPRoutesHTML(rc, entries, 10); !strings.Contains(html, "<strong>代码级时序</strong>") {
		t.Errorf("HTML 版 HTTP 入口应走代码级时序块:\n%s", html)
	}
	// 无 handler_id（匿名/方法值）→ fallback 索引调用链（不出现代码级时序标题）
	rc2 := &wikiRenderCtx{acts: acts, RepoAbs: dir, SeqDepth: 3, Diagram: "mermaid"}
	md2 := renderHTTPRoutesMD(rc2, []httpProcEntry{{Handler: "(匿名)", Paths: []string{"GET /x"}}}, 10)
	if strings.Contains(md2, "**代码级时序**（源码 AST") {
		t.Errorf("无 handler_id 应 fallback 调用链（无代码级时序块）:\n%s", md2)
	}
}
