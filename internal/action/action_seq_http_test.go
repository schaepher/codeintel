package action

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func httpTestActions(t *testing.T, files map[string]string, nodes []*domain.CodeEntity) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

func routeNode(path, method, handler, register string) *domain.CodeEntity {
	return &domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/srv:route." + path),
		Kind: domain.KindHTTPRoute, Name: "route." + path,
		Properties: map[string]any{"path": path, "method": method,
			"handler": handler, "handler_id": handler, "register": register}}
}

func TestParseSeqHTTPEndpoint(t *testing.T) {
	ok, err := parseSeqHTTPEndpoint("http://127.0.0.1:18443/submit?x=1", "POST")
	if err != nil || ok.host != "127.0.0.1:18443" || ok.path != "/submit" || ok.method != "POST" {
		t.Fatalf("合法 HTTP URL 解析失败: %+v %v", ok, err)
	}
	bad := []string{"", "2026-01-01T00:00:00Z"}
	for _, raw := range bad {
		if _, err := parseSeqHTTPEndpoint(raw, "GET"); err != nil {
			continue
		}
		t.Errorf("普通字符串 %q 不应被当成 HTTP endpoint", raw)
	}
	for _, raw := range []string{"https://127.0.0.1:18443/submit", "/submit", "127.0.0.1:18443/submit"} {
		if _, err := parseSeqHTTPEndpoint(raw, "GET"); err == nil {
			t.Errorf("%q 应为 endpoint_unknown（非 http 绝对地址）", raw)
		}
	}
}

func TestSeqHTTPOriginProofMatrix(t *testing.T) {
	const handler = "symbol:go:example.com/srv:Submit"
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(handler), Kind: domain.KindFunction, Name: "Submit",
			FilePath: "server.go", LineStart: 10},
		routeNode("/submit", "", handler, "server.go:6"),
	}
	acts, dir := httpTestActions(t, map[string]string{
		"server.go": `package srv

import "net/http"

func Serve() {
	http.HandleFunc("/submit", Submit)
	http.ListenAndServe("127.0.0.1:18443", nil)
}

func Submit(w http.ResponseWriter, r *http.Request) {}
`,
	}, nodes)

	ep := seqHTTPEndpoint{method: "POST", host: "127.0.0.1:18443", path: "/submit"}
	got, why, incomplete := seqHTTPProvenTargets(acts, CodeSequenceRequest{RepoAbs: dir}, ep, "client.go", 4)
	if len(got) != 1 || incomplete || why != "" {
		t.Fatalf("正例应证明唯一 handler: targets=%+v why=%q incomplete=%v", got, why, incomplete)
	}
	if got[0].handler != handler || got[0].file != "server.go" || got[0].line != 10 {
		t.Errorf("handler 身份/位置不准确: %+v", got[0])
	}
	// 端口不符 → 不证明，且明确 target_unproven。
	ep.host = "127.0.0.1:9999"
	got, why, _ = seqHTTPProvenTargets(acts, CodeSequenceRequest{RepoAbs: dir}, ep, "client.go", 4)
	if len(got) != 0 || why == "" {
		t.Errorf("端口不符应 unproven: targets=%+v why=%q", got, why)
	}
}

func TestSeqHTTPOriginRequiresDirectListen(t *testing.T) {
	const handler = "symbol:go:example.com/srv:Submit"
	acts, dir := httpTestActions(t, map[string]string{
		"server.go": `package srv

import "net/http"

func Serve(cond bool) {
	http.HandleFunc("/submit", Submit)
	if cond {
		http.ListenAndServe("127.0.0.1:18443", nil)
	}
}

func Submit(w http.ResponseWriter, r *http.Request) {}
`,
	}, []*domain.CodeEntity{
		{ID: domain.CanonicalID(handler), Kind: domain.KindFunction, Name: "Submit",
			FilePath: "server.go", LineStart: 11},
		routeNode("/submit", "", handler, "server.go:6"),
	})
	ep := seqHTTPEndpoint{method: "POST", host: "127.0.0.1:18443", path: "/submit"}
	got, why, _ := seqHTTPProvenTargets(acts, CodeSequenceRequest{RepoAbs: dir}, ep, "client.go", 4)
	if len(got) != 0 || why == "" {
		t.Errorf("非直接语句的 ListenAndServe 不得证明 origin: targets=%+v why=%q", got, why)
	}
}

func TestSeqHTTPOriginAmbiguous(t *testing.T) {
	const h1 = "symbol:go:example.com/srv:Submit"
	const h2 = "symbol:go:example.com/srv:Submit2"
	acts, dir := httpTestActions(t, map[string]string{
		"a.go": `package srv

import "net/http"

func ServeA() {
	http.HandleFunc("/submit", Submit)
	http.ListenAndServe("127.0.0.1:18443", nil)
}

func Submit(w http.ResponseWriter, r *http.Request) {}
`,
		"b.go": `package srv

import "net/http"

func ServeB() {
	http.HandleFunc("/submit", Submit2)
	http.ListenAndServe("127.0.0.1:18443", nil)
}

func Submit2(w http.ResponseWriter, r *http.Request) {}
`,
	}, []*domain.CodeEntity{
		{ID: domain.CanonicalID(h1), Kind: domain.KindFunction, Name: "Submit", FilePath: "a.go", LineStart: 10},
		{ID: domain.CanonicalID(h2), Kind: domain.KindFunction, Name: "Submit2", FilePath: "b.go", LineStart: 10},
		routeNode("/submit", "", h1, "a.go:6"),
		routeNode("/submit2", "", h2, "b.go:6"),
	})
	// 两条 route 节点 path 不同；第二个同 path 的注册由 routes.yaml 形态模拟。
	ep := seqHTTPEndpoint{method: "POST", host: "127.0.0.1:18443", path: "/submit"}
	got, _, _ := seqHTTPProvenTargets(acts, CodeSequenceRequest{RepoAbs: dir}, ep, "client.go", 4)
	if len(got) != 1 {
		t.Fatalf("前置：/submit 应唯一证明，got %+v", got)
	}
	// 同 path 的第二条注册（真实 emitter 同 path 冲突；这里直接改节点属性）
	acts.repo = &twoRouteRepo{Reader: acts.repo}
	got, why, _ := seqHTTPProvenTargets(acts, CodeSequenceRequest{RepoAbs: dir}, ep, "client.go", 4)
	if len(got) != 2 || why != "" {
		t.Fatalf("两个满足证明的注册应返回歧义候选: targets=%+v why=%q", got, why)
	}
}

// twoRouteRepo 在 /submit 上再补一条同 path 注册（真实索引允许同 path 两次
// HandleFunc；emitter 会发两个节点）。
type twoRouteRepo struct {
	Reader
}

func (r *twoRouteRepo) GetHTTPRouteNodes() ([]*domain.CodeEntity, error) {
	nodes, err := r.Reader.GetHTTPRouteNodes()
	if err != nil {
		return nil, err
	}
	return append(nodes, routeNode("/submit", "", "symbol:go:example.com/srv:Submit2", "b.go:6")), nil
}
