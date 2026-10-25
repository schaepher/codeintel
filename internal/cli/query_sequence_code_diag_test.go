package cli

// todo10-3 渲染层：展开终点诊断与"多候选未消歧"必须对用户可见
// （JSON 树里有，文本/mermaid 也要能看出来）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedSeqDiagCLI 建 fixture：main → leaf（叶子）/ gone（源码缺失）。
func seedSeqDiagCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

func leaf() {}

func main() {
	leaf()
	gone()
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
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:leaf", Kind: domain.KindFunction, Name: "leaf", FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:gone", Kind: domain.KindFunction, Name: "gone", FilePath: "gone.go", LineStart: 1},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m:leaf",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(6)}},
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m:gone",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(7)}},
	}
	if _, err := sqlite.NewRepo(db).SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSequenceCodeDiagVisible：文本模式附"展开终点诊断"节（含 file:line 与
// code），mermaid 用 Note 标注未继续展开原因；JSON meta.diagnostics 聚合。
func TestSequenceCodeDiagVisible(t *testing.T) {
	dir := seedSeqDiagCLI(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "example.com/m:main", "--code", "--repo", dir, "--depth", "2"}); code != 0 {
			t.Errorf("sequence --code exit = %d", code)
		}
	})
	for _, want := range []string{"展开终点诊断", "source_missing", "main.go:7"} {
		if !strings.Contains(out, want) {
			t.Errorf("文本输出应含 %q:\n%s", want, out)
		}
	}

	mermaid := captureStdout(func() {
		cmdQuery([]string{"sequence", "example.com/m:main", "--code", "--format", "mermaid", "--repo", dir, "--depth", "2"})
	})
	if !strings.Contains(mermaid, "未继续展开：source_missing") {
		t.Errorf("mermaid 应用 Note 标出未展开原因:\n%s", mermaid)
	}

	jsonOut := captureStdout(func() {
		cmdQuery([]string{"sequence", "example.com/m:main", "--code", "--json", "--repo", dir, "--depth", "2"})
	})
	for _, want := range []string{`"diagnostics"`, `"source_missing"`, `"evidence"`, `"call_file"`} {
		if !strings.Contains(jsonOut, want) {
			t.Errorf("JSON 应含 %q:\n%s", want, jsonOut)
		}
	}
}

// TestSequenceCodeDiagError：根不可用时给出可操作错误（不是笼统"不可用"）。
func TestSequenceCodeDiagError(t *testing.T) {
	dir := seedSeqDiagCLI(t)
	var code int
	stderr := captureStderr(func() {
		code = cmdQuery([]string{"sequence", "example.com/m:gone", "--code", "--repo", dir})
	})
	if code != 1 {
		t.Errorf("exit = %d; want 1", code)
	}
	if !strings.Contains(stderr, "源码文件读不到") || !strings.Contains(stderr, "update") {
		t.Errorf("错误应说明原因并给出下一步:\n%s", stderr)
	}
}

// TestSequenceCodeEvidenceIdentity：图形展示（mermaid）与结构化结果使用
// 同一证据身份——`%% evidence` 行含调用点位置 + canonical ID + verdict；
// `--evidence` 文本附录逐箭头列出可核验来源（todo10-8）。
func TestSequenceCodeEvidenceIdentity(t *testing.T) {
	dir := seedSeqDiagCLI(t)
	mermaid := captureStdout(func() {
		cmdQuery([]string{"sequence", "example.com/m:main", "--code", "--format", "mermaid", "--repo", dir, "--depth", "2"})
	})
	for _, want := range []string{"%% evidence", "id=symbol:go:example.com/m:leaf", "verdict=", "main.go:6"} {
		if !strings.Contains(mermaid, want) {
			t.Errorf("mermaid 证据注释缺 %q:\n%s", want, mermaid)
		}
	}

	out := captureStdout(func() {
		cmdQuery([]string{"sequence", "example.com/m:main", "--code", "--repo", dir, "--depth", "2", "--evidence"})
	})
	if !strings.Contains(out, "箭头证据") {
		t.Fatalf("--evidence 应输出证据清单:\n%s", out)
	}
	for _, want := range []string{"source=", "verdict=", "id=symbol:go:example.com/m:leaf", "codeintel query sequence"} {
		if !strings.Contains(out, want) {
			t.Errorf("证据清单缺 %q:\n%s", want, out)
		}
	}
	// 与 mermaid 同一身份：两边都出现同一个 canonical ID
	if !strings.Contains(mermaid, "symbol:go:example.com/m:leaf") || !strings.Contains(out, "symbol:go:example.com/m:leaf") {
		t.Error("结构化结果与图形展示的证据身份不一致")
	}
}
