package action

// T11/U07：HTTP 跨节点续接——传输调用保留 net/http 身份，服务端 handler 是
// 单独一步（"HTTP POST <url>"），handler 内部调用的发送方是其自身节点
// （不出现空发送方）；两端参与者/canonical ID 都非空。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestSeqHTTPTransportAndHandlerSplit(t *testing.T) {
	const (
		pkg     = "symbol:go:example.com/srv:"
		handler = pkg + "Submit"
		persist = pkg + "Persist"
	)
	dir := t.TempDir()
	src := `package srv

import "net/http"

func Entry() { RawPostWithSign("http://127.0.0.1:18081/submit") }

func RawPostWithSign(uri string) { http.Post(uri, "application/json", nil) }

func Setup() {
	http.HandleFunc("/submit", Submit)
	http.ListenAndServe("127.0.0.1:18081", nil)
}

func Submit(w http.ResponseWriter, r *http.Request) { Persist() }

func Persist() {}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/srv\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "Entry"), Kind: domain.KindFunction, Name: "Entry", FilePath: "flow.go", LineStart: 5},
		{ID: domain.CanonicalID(pkg + "RawPostWithSign"), Kind: domain.KindFunction, Name: "RawPostWithSign", FilePath: "flow.go", LineStart: 7},
		{ID: domain.CanonicalID(pkg + "Submit"), Kind: domain.KindFunction, Name: "Submit", FilePath: "flow.go", LineStart: 14},
		{ID: domain.CanonicalID(pkg + "Persist"), Kind: domain.KindFunction, Name: "Persist", FilePath: "flow.go", LineStart: 16},
		{ID: "symbol:go:net/http:Post", Kind: domain.KindFunction, Name: "Post"},
		routeNode("/submit", "", handler, "flow.go:10"),
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(pkg + "Entry"), TargetID: domain.CanonicalID(pkg + "RawPostWithSign"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 5}},
		{SourceID: domain.CanonicalID(pkg + "Submit"), TargetID: domain.CanonicalID(pkg + "Persist"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 14}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	acts := New(r)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: pkg + "Entry", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	// 传输节点：net/http:Post（corroborated），不是 handler 身份。
	transport := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Label == "http.Post"
	})
	if len(transport) != 1 {
		t.Fatalf("应有 1 个 http.Post 传输节点，got %d", len(transport))
	}
	tp := transport[0]
	if tp.Evidence == nil || tp.Evidence.SymbolID != "symbol:go:net/http:Post" ||
		seqEvidenceVerdict(tp.Evidence) != SeqVerdictCorroborated {
		t.Fatalf("传输节点身份/verdict 不符: %+v", tp.Evidence)
	}
	// handler 节点：单独一步，带 URL 与准确 canonical ID。
	if len(tp.Nodes) == 0 {
		t.Fatalf("传输节点下应有服务端 handler 步骤")
	}
	h := tp.Nodes[0]
	if h.Actor == "" || !strings.HasPrefix(h.Label, "HTTP POST http://127.0.0.1:18081/submit") {
		t.Fatalf("handler 步骤缺发送方/URL: %+v", h)
	}
	if h.Evidence == nil || h.Evidence.SymbolID != handler ||
		seqEvidenceVerdict(h.Evidence) != SeqVerdictCorroborated {
		t.Fatalf("handler 身份不符: %+v", h.Evidence)
	}
	// handler 内部调用从 handler 节点发出（证据两端非空、verdict=direct）。
	var persistNode *CodeSeqNode
	for _, n := range h.Nodes {
		if n.Kind == "call" && n.Label == "Persist" {
			persistNode = n
		}
	}
	if persistNode == nil {
		t.Fatalf("handler 内应展开 Persist: %+v", h.Nodes)
	}
	if persistNode.Evidence == nil || persistNode.Evidence.SymbolID != persist ||
		seqEvidenceVerdict(persistNode.Evidence) != SeqVerdictDirect {
		t.Fatalf("Persist 证据不完整: %+v", persistNode.Evidence)
	}
	// 传输与 handler 必须是不同参与者（Mermaid 不能共用一个 participant）。
	if tp.Actor == h.Actor {
		t.Errorf("HTTP 传输与 Submit 不应共用参与者: %q", tp.Actor)
	}
}
