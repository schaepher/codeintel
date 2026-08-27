package cli

// R49 query grpc-composites 测试：接口完整包含 pb server 接口（方法名
// 超集）→ pb_servers 属性；非超集接口不标注。测试先行。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedGrpcCompositesRepo：组合接口（完整包含两个 server 接口）+ 非组合
// 接口（方法集不完整）。
func seedGrpcCompositesRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		// 组合接口：方法集 = 两个 server 接口方法全集（+ 额外方法）
		{ID: "symbol:go:example.com/m/app:AllService", Kind: domain.KindInterface,
			Name: "AllService", FilePath: "app/all.go", LineStart: 10,
			Properties: map[string]any{"pb_servers": "example.com/m/api.PingServer,example.com/m/api.EchoServer"}},
		// 非组合接口：方法集不完整——不标注
		{ID: "symbol:go:example.com/m/app:PartialService", Kind: domain.KindInterface,
			Name: "PartialService", FilePath: "app/all.go", LineStart: 20},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	return dir
}

// TestGrpcComposites：组合接口列出（servers 完整）+ 非组合不出现。
func TestGrpcComposites(t *testing.T) {
	dir := seedGrpcCompositesRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := action.New(sqlite.NewRepo(db)).GrpcComposites()
	if err != nil {
		t.Fatalf("GrpcComposites: %v", err)
	}
	if len(res.Composites) != 1 {
		t.Fatalf("组合接口数 = %d; want 1（非组合不应列出）:\n%+v", len(res.Composites), res.Composites)
	}
	c := res.Composites[0]
	if !strings.Contains(c.Iface, "AllService") {
		t.Errorf("iface = %q; want AllService", c.Iface)
	}
	if len(c.Servers) != 2 || c.Servers[0] != "example.com/m/api.PingServer" || c.Servers[1] != "example.com/m/api.EchoServer" {
		t.Errorf("servers = %v; want PingServer,EchoServer", c.Servers)
	}
	if !strings.Contains(c.Loc, "app/all.go") {
		t.Errorf("loc = %q; want app/all.go", c.Loc)
	}
}

// TestCmdGrpcComposites：CLI 输出。
func TestCmdGrpcComposites(t *testing.T) {
	dir := seedGrpcCompositesRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	out := captureStdout(func() {
		if code := cmdGrpcComposites(acts, queryFlags{}); code != 0 {
			t.Errorf("cmdGrpcComposites = %d; want 0", code)
		}
	})
	for _, want := range []string{"AllService", "PingServer", "EchoServer", "完整包含"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应含 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "PartialService") {
		t.Errorf("非组合接口不应出现:\n%s", out)
	}
}
