package cli

// todo10-5：--scene 在 CLI 的端到端效果——文本标注场景判定 + 汇总；
// mermaid 用 Note 标出；不同条件得到不同路径。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestSequenceCodeSceneFlag：--scene region=CN 时文本标注 taken/excluded
// 并输出场景汇总；未指定时不标注。
func TestSequenceCodeSceneFlag(t *testing.T) {
	dir := seedSeqSceneCLI(t)

	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "example.com/m:Route", "--code", "--repo", dir,
			"--scene", "region=CN", "--depth", "1"}); code != 0 {
			t.Errorf("sequence --code --scene exit = %d", code)
		}
	})
	for _, want := range []string{"[scene:taken", "[scene:excluded", "场景条件", "taken="} {
		if !strings.Contains(out, want) {
			t.Errorf("文本输出应含 %q:\n%s", want, out)
		}
	}

	plain := captureStdout(func() {
		cmdQuery([]string{"sequence", "example.com/m:Route", "--code", "--repo", dir, "--depth", "1"})
	})
	if strings.Contains(plain, "[scene:") || strings.Contains(plain, "场景条件") {
		t.Errorf("未指定 --scene 不应出现场景标注:\n%s", plain)
	}

	mermaid := captureStdout(func() {
		cmdQuery([]string{"sequence", "example.com/m:Route", "--code", "--format", "mermaid",
			"--repo", dir, "--scene", "region=CN", "--depth", "1"})
	})
	if !strings.Contains(mermaid, "场景判定 taken") {
		t.Errorf("mermaid 应用 Note 标出场景判定:\n%s", mermaid)
	}

	js := captureStdout(func() {
		cmdQuery([]string{"sequence", "example.com/m:Route", "--code", "--json", "--repo", dir,
			"--scene", "region=CN", "--depth", "1"})
	})
	for _, want := range []string{`"scene"`, `"status": "taken"`, `"expected": "CN"`, `"scene": {`} {
		if !strings.Contains(js, want) {
			t.Errorf("JSON 应含 %q:\n%s", want, js)
		}
	}
}

// seedSeqSceneCLI 建两分支 fixture（Route(region) → notifyCN/notifyUS）。
func seedSeqSceneCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

func notifyCN() {}

func notifyUS() {}

func Route(region string) {
	if region == "CN" {
		notifyCN()
	}
	if region != "CN" {
		notifyUS()
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := sqlite.NewRepo(db).SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Route", Kind: domain.KindFunction, Name: "Route", FilePath: "main.go", LineStart: 7},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
