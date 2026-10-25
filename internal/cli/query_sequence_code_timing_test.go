package cli

// todo10-9：`query sequence --code` 的阶段计时诊断只写 stderr / meta，
// 默认 stdout 的 JSON 契约不变；取消不报告为成功。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seqTimingCLIFixture 建最小仓库（main → Run），并写入索引节点。
func seqTimingCLIFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type Repo struct{}

func (r *Repo) Load() error { return nil }

type Service struct{ repo *Repo }

func (s *Service) Run() error { return s.repo.Load() }

func main() { _ = (&Service{}).Run() }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return seedCodeSeqNodes(t, dir)
}

// seedCodeSeqNodes 写入 main → (Service).Run 的节点与调用边，返回仓库目录。
func seedCodeSeqNodes(t *testing.T, dir string) string {
	t.Helper()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 11},
		{ID: "symbol:go:example.com/m:(Service).Run", Kind: domain.KindMethod, Name: "(Service).Run", FilePath: "main.go", LineStart: 9},
	}
	facts := []*domain.Fact{{
		SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m:(Service).Run",
		Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
		Metadata: map[string]any{"line_num": float64(11)},
	}}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestQuerySequenceCodeTimingDiagnostics：开 CODEINTEL_SEQ_TIMING 时
// stderr 有阶段行（resolve/read/walk/render/nodes/stop），stdout 的 JSON
// 带 meta.timing；关闭时 JSON 不含 timing（契约不变）。
func TestQuerySequenceCodeTimingDiagnostics(t *testing.T) {
	dir := seqTimingCLIFixture(t)

	t.Setenv("CODEINTEL_SEQ_TIMING", "1")
	var code int
	stderr := captureStderr(func() {
		out := captureStdout(func() {
			code = cmdQuery([]string{"sequence", "example.com/m:main", "--code", "--json", "--repo", dir, "--depth", "2"})
		})
		if code != 0 {
			t.Errorf("sequence --code --json exit = %d", code)
		}
		for _, want := range []string{`"meta"`, `"timing"`, `"resolve_ms"`, `"read_ms"`, `"walk_ms"`, `"render_ms"`, `"nodes"`, `"stop"`} {
			if !strings.Contains(out, want) {
				t.Errorf("开启诊断时 JSON 应含 %s:\n%s", want, out)
			}
		}
	})
	for _, want := range []string{"[seq]", "resolve=", "read=", "walk=", "render=", "nodes=", "stop="} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr 应含阶段诊断 %q:\n%s", want, stderr)
		}
	}

	// 关闭开关：JSON 不含 timing（既有契约/输出体积不变）
	t.Setenv("CODEINTEL_SEQ_TIMING", "")
	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "example.com/m:main", "--code", "--json", "--repo", dir}); code != 0 {
			t.Errorf("sequence --code --json exit = %d", code)
		}
	})
	if strings.Contains(out, `"timing"`) {
		t.Errorf("默认（开关关闭）JSON 不应含 timing:\n%s", out)
	}
	if !strings.Contains(out, `"seq_version"`) {
		t.Errorf("meta.seq_version 应始终存在（契约版本）:\n%s", out)
	}
}
