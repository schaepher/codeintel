package action

// U06d：vendor 源码包身份校验（只测源码身份；不冒充调用图解析）。

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedVendorRepo 构造 app module + vendor 依赖与节点（文件位置 = vendor 路径）。
func seedVendorRepo(t *testing.T) (*Actions, string, *sqlite.Repo) {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	writeVendorFixture(t, root, app)
	db, err := sqlite.Open(app)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	vendFile := "vendor/example.com/vendorlib/dep.go"
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/vendorlib:(Store).Run", Kind: domain.KindMethod,
			Name: "(Store).Run", FilePath: vendFile, LineStart: 2},
		{ID: "symbol:go:example.com/vendorapp/vendor/example.com/vendorlib:(Store).Run",
			Kind: domain.KindMethod, Name: "(Store).Run", FilePath: vendFile, LineStart: 2},
		{ID: "symbol:go:example.com/spoof:(Store).Run", Kind: domain.KindMethod,
			Name: "(Store).Run", FilePath: vendFile, LineStart: 2},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), app, r
}

func writeVendorFixture(t *testing.T, root, app string) {
	t.Helper()
	writeAbs := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeAbs(filepath.Join(root, "dep", "go.mod"), "module example.com/vendorlib\n\ngo 1.26.2\n")
	writeAbs(filepath.Join(root, "dep", "dep.go"), "package vendorlib\ntype Store struct{}\nfunc(*Store)Run(){}\n")
	writeBlock := func(rel, content string) {
		t.Helper()
		p := filepath.Join(app, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeBlock("go.mod", "module example.com/vendorapp\n\ngo 1.26.2\n\nrequire example.com/vendorlib v0.0.0\n\nreplace example.com/vendorlib => ../dep\n")
	writeBlock("app.go", "package vendorapp\n\nimport \"example.com/vendorlib\"\n\nfunc Entry(){(&vendorlib.Store{}).Run()}\n")
	// 用真实 `go mod vendor` 生成 vendor 目录与 modules.txt（离线只读）。
	cmd := exec.Command("go", "mod", "vendor")
	cmd.Dir = app
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod vendor: %v\n%s", err, out)
	}
}

// TestSymbolSourceVendorIdentity 准确 vendor ID 通过；伪身份逐项拒绝。
func TestSymbolSourceVendorIdentity(t *testing.T) {
	a, app, _ := seedVendorRepo(t)
	got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: app,
		SymbolID: "symbol:go:example.com/vendorlib:(Store).Run"})
	if err != nil {
		t.Fatalf("准确 vendor ID 应通过: %v", err)
	}
	if got.Package != "example.com/vendorlib" ||
		got.File != "vendor/example.com/vendorlib/dep.go" || got.Location != "repository" {
		t.Fatalf("source: %+v", got)
	}
	for _, id := range []string{
		"symbol:go:example.com/vendorapp/vendor/example.com/vendorlib:(Store).Run",
		"symbol:go:example.com/spoof:(Store).Run",
	} {
		if _, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: app, SymbolID: id}); err == nil {
			t.Fatalf("%s 应被拒绝（包身份不匹配）", id)
		}
	}
	// 删除 modules.txt → 拒绝。
	modTxt := filepath.Join(app, "vendor", "modules.txt")
	if err := os.Remove(modTxt); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: app,
		SymbolID: "symbol:go:example.com/vendorlib:(Store).Run"}); err == nil {
		t.Fatalf("缺 modules.txt 应被拒绝")
	}
}

// TestSymbolSourceVendorSpoofedManifest modules.txt 登记别的模块 → 拒绝。
func TestSymbolSourceVendorSpoofedManifest(t *testing.T) {
	a, app, _ := seedVendorRepo(t)
	if err := os.WriteFile(filepath.Join(app, "vendor", "modules.txt"),
		[]byte("# example.com/other v0.0.0\nexample.com/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: app,
		SymbolID: "symbol:go:example.com/vendorlib:(Store).Run"}); err == nil {
		t.Fatalf("modules.txt 未登记该包应被拒绝")
	}
}

// TestSymbolSourceVendorExcludedFile 构建标签排除的文件不供声明。
func TestSymbolSourceVendorExcludedFile(t *testing.T) {
	a, app, repo := seedVendorRepo(t)
	excluded := filepath.Join(app, "vendor", "example.com", "vendorlib", "excluded.go")
	content := "//go:build never_source_fixture\n\npackage vendorlib\n\nfunc Excluded(){}\n"
	if err := os.WriteFile(excluded, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// 直接在已有 DB 里补节点（复用 seed 的 repo）。
	nodes := []*domain.CodeEntity{{ID: "symbol:go:example.com/vendorlib:Excluded",
		Kind: domain.KindFunction, Name: "Excluded",
		FilePath: "vendor/example.com/vendorlib/excluded.go", LineStart: 5}}
	if _, err := repo.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: app,
		SymbolID: "symbol:go:example.com/vendorlib:Excluded"}); err == nil {
		t.Fatalf("go list 未选中的文件应被拒绝")
	}
}
