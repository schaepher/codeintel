//go:build integration

package integration

// todo25 §4.3：标准 HTTP 的 origin / 注册 / 准确续接（真实 init → CLI）。
// 公开输入见 todo25.txt：client=example.com/client，server=example.com/server。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25ClientGoMod = "module example.com/client\n\ngo 1.26.2\n"
const todo25ServerGoMod = "module example.com/server\n\ngo 1.26.2\n"

const todo25ClientSrc = `package client

import "net/http"

func RawPostWithSign(uri string) { http.Post(uri, "application/json", nil) }

func Entry() { RawPostWithSign("http://127.0.0.1:18443/submit") }
`

const todo25ServerSrc = `package server

import "net/http"

func Serve() {
	http.HandleFunc("/submit", Submit)
	http.ListenAndServe("127.0.0.1:18443", nil)
}

func Submit(w http.ResponseWriter, r *http.Request) { Persist() }
func Persist() {}
`

// todo25HTTPFixture 建 client/server 两个模块 + workspace，真实 init。
func todo25HTTPFixture(t *testing.T) (root, client, server string) {
	t.Helper()
	root = t.TempDir()
	client = filepath.Join(root, "client")
	server = filepath.Join(root, "server")
	writeFile(t, filepath.Join(client, "go.mod"), todo25ClientGoMod)
	writeFile(t, filepath.Join(client, "client.go"), todo25ClientSrc)
	writeFile(t, filepath.Join(server, "go.mod"), todo25ServerGoMod)
	writeFile(t, filepath.Join(server, "server.go"), todo25ServerSrc)
	writeFile(t, filepath.Join(server, "extra.go"), `package server

import "net/http"

func Extra(w http.ResponseWriter, r *http.Request) {}

func ServeExtra() {
	http.HandleFunc("/extra", Extra)
	http.ListenAndServe("127.0.0.1:18443", nil)
}
`)
	for _, d := range []string{client, server} {
		gitRun(t, d, "init")
		gitRun(t, d, "add", "-A")
		gitRun(t, d, "commit", "-m", "init")
	}
	for _, d := range []string{client, server} {
		clearIndex(t, d)
		if code := runCLI(t, "init", "--repo", d, "--workers", "1", "--progress", "none"); code != 0 {
			t.Fatalf("init %s exit=%d", d, code)
		}
	}
	ws := filepath.Join(root, "ws")
	if code := runCLI(t, "workspace", "init", "--dir", ws,
		"--repo", client, "--repo", server, "--build"); code != 0 {
		t.Fatalf("workspace init exit=%d", code)
	}
	return root, filepath.Join(ws, "client"), filepath.Join(ws, "server")
}

func TestTodo25HTTPContinuationRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	_, client, server := todo25HTTPFixture(t)
	root := seqTreeJSON(t, client, "symbol:go:example.com/client:Entry", 12)
	var httpNode *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == action.SeqEvidenceHTTPServer {
			httpNode = n
		}
	})
	if httpNode == nil {
		t.Fatalf("未找到 HTTP 续接节点: %+v", root)
	}
	ev := httpNode.Evidence
	if ev.SymbolID != "symbol:go:example.com/server:Submit" {
		t.Errorf("HTTP handler = %q; want server:Submit", ev.SymbolID)
	}
	if ev.Reason != action.SeqReasonHTTPOrigin {
		t.Errorf("reason = %q; want %q", ev.Reason, action.SeqReasonHTTPOrigin)
	}
	if ev.RemoteRepo != server {
		t.Errorf("remote_repo = %q; want %q", ev.RemoteRepo, server)
	}
	if !hasSymbolWith(httpNode, "server:Persist") {
		t.Errorf("HTTP 子树应含 Persist: %+v", httpNode.Nodes)
	}
	if !hasSymbolWith(root, "client:RawPostWithSign") {
		t.Errorf("链应保留 transport 调用 RawPostWithSign")
	}

	// 同包不同文件的两条注册都必须真实存在（旧 ID 每文件自增 route.1 会
	// 被 UPSERT 合并成一条）。
	code, out := runCLIOut(t, "query", "http-routes", "--repo", server, "--json")
	if code != 0 {
		t.Fatalf("http-routes exit=%d: %s", code, out)
	}
	var routes action.HTTPRoutesResult
	if err := json.Unmarshal([]byte(out), &routes); err != nil {
		t.Fatalf("http-routes JSON: %v", err)
	}
	paths := map[string]bool{}
	for _, r := range routes.Routes {
		if r.Direction == action.HTTPRouteInbound {
			paths[r.Path] = true
		}
	}
	if !paths["/submit"] || !paths["/extra"] {
		t.Errorf("同包不同文件的两条注册都应存在: %+v", routes.Routes)
	}

	// 浅查询：保留同一 Submit ID + depth 诊断。
	shallow := seqTreeJSON(t, client, "symbol:go:example.com/client:Entry", 2)
	var shallowHTTP *action.CodeSeqNode
	walkSeq(shallow, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == action.SeqEvidenceHTTPServer {
			shallowHTTP = n
		}
	})
	if shallowHTTP == nil || shallowHTTP.Evidence.SymbolID != "symbol:go:example.com/server:Submit" {
		t.Fatalf("depth=1 应保留同一 handler ID: %+v", shallowHTTP)
	}
	if shallowHTTP.Stop == nil || shallowHTTP.Stop.Code != action.SeqStopDepthLimit {
		t.Errorf("depth=1 HTTP 节点应停在 depth: %+v", shallowHTTP.Stop)
	}

	// 跨仓续查：depth=2 + 16 段仍能接上 Persist。
	continued := seqTreeJSON(t, client, "symbol:go:example.com/client:Entry", 2,
		"--continue-segments", "16")
	if !hasSymbolWith(continued, "server:Persist") {
		t.Errorf("HTTP 跨仓续查应接到 Persist: %+v", continued)
	}

	// workspace 外：起始仓库无 workspace 归属 → 只查本仓，服务端未证明。
	mainClient := filepath.Join(filepath.Dir(client), "..", "client")
	outside := seqTreeJSON(t, mainClient, "symbol:go:example.com/client:Entry", 12)
	var outsideHTTP *action.CodeSeqNode
	walkSeq(outside, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == action.SeqEvidenceHTTPServer {
			outsideHTTP = n
		}
	})
	if outsideHTTP != nil && outsideHTTP.Stop != nil &&
		outsideHTTP.Stop.Code != action.SeqStopHTTPTargetUnproven {
		t.Errorf("workspace 外应为 http_target_unproven: %+v", outsideHTTP.Stop)
	}
	if outsideHTTP != nil && hasSymbolWith(outsideHTTP, "server:Submit") {
		t.Errorf("workspace 外不得续接到 server:Submit")
	}
}

func TestTodo25HTTPEndpointAndWorkspaceBoundaries(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	// 动态 URL：不进入 handler。
	root := t.TempDir()
	client := filepath.Join(root, "client")
	writeFile(t, filepath.Join(client, "go.mod"), todo25ClientGoMod)
	writeFile(t, filepath.Join(client, "client.go"), `package client

import "net/http"

func Entry(uri string) { http.Post(uri, "application/json", nil) }
`)
	gitRun(t, client, "init")
	gitRun(t, client, "add", "-A")
	gitRun(t, client, "commit", "-m", "init")
	clearIndex(t, client)
	if code := runCLI(t, "init", "--repo", client, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	tree := seqTreeJSON(t, client, "symbol:go:example.com/client:Entry", 4)
	var stop *action.SeqStop
	walkSeq(tree, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == action.SeqEvidenceHTTPServer {
			stop = n.Stop
		}
	})
	if stop == nil || stop.Code != action.SeqStopHTTPEndpointUnknown {
		t.Errorf("动态 URL 应为 endpoint_unknown: %+v", stop)
	}
	if strings.Contains(strings.ToLower(stop.Detail), "submit") {
		t.Errorf("不得伪造 handler: %+v", stop)
	}
}
