//go:build integration

package integration

// todo22 §6：`query symbol <canonical-id> --include-source --json` 的真实
// CLI 验收——真实 init（不手工造图）→ 按 canonical ID 取声明源码。
//
// 断言：
//   - source 对象五个字段与 todo22 §6 的期望逐字节相同（file/line_start/
//     line_end/text；text 为 flow.go 原样字节，不做格式化）；
//   - 不加 --include-source 时输出契约不变（没有 source 键）；
//   - 明确失败的输入：不支持的声明种类（struct）——非零退出且 stdout 为空
//     （不输出部分成功 JSON）。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type symbolSourceJSON struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Source *struct {
		SymbolID  string `json:"symbol_id"`
		File      string `json:"file"`
		LineStart int    `json:"line_start"`
		LineEnd   int    `json:"line_end"`
		Text      string `json:"text"`
	} `json:"source"`
}

// TestTodo22SymbolSourceRealCLI：真实 init → --include-source。
func TestTodo22SymbolSourceRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(wd)
	bin := filepath.Join(t.TempDir(), "codeintel")
	build := exec.Command("go", "build", "-o", bin, "./cmd/codeintel")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build codeintel: %v\n%s", err, out)
	}
	fixture := writeFlowFixture(t)
	run := func(args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		return code, stdout.String(), stderr.String()
	}

	if code, out, errOut := run("init", "--repo", fixture); code != 0 {
		t.Fatalf("init exit = %d\n%s\n%s", code, out, errOut)
	}

	const storeInsert = "symbol:go:example.com/flow:(Store).Insert"
	code, out, errOut := run("query", "symbol", storeInsert,
		"--include-source", "--json", "--repo", fixture)
	if code != 0 {
		t.Fatalf("--include-source exit = %d\n%s", code, errOut)
	}
	var got symbolSourceJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON 不可解码: %v\n%s", err, out)
	}
	if got.Source == nil {
		t.Fatalf("JSON 缺少 source 对象:\n%s", out)
	}
	wantText := "func (s *Store) Insert(ctx context.Context) error {\n" +
		"    _, err := s.DB.ExecContext(ctx, \"INSERT INTO records(id) VALUES (?)\", 1)\n" +
		"    return err\n" +
		"}"
	if got.Source.SymbolID != storeInsert {
		t.Errorf("source.symbol_id = %q，期望 %q", got.Source.SymbolID, storeInsert)
	}
	if got.Source.File != "flow.go" {
		t.Errorf("source.file = %q，期望 flow.go", got.Source.File)
	}
	if got.Source.LineStart != 15 || got.Source.LineEnd != 18 {
		t.Errorf("source 行号 = %d-%d，期望 15-18（flow.go 原样保存的 AST 实际位置）",
			got.Source.LineStart, got.Source.LineEnd)
	}
	if got.Source.Text != wantText {
		t.Errorf("source.text 与 flow.go 声明逐字节不符:\n got %q\nwant %q",
			got.Source.Text, wantText)
	}
	// text 必须就是源文件的字节切片（不是重新格式化/拼接）
	full, err := os.ReadFile(filepath.Join(fixture, "flow.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full), got.Source.Text) {
		t.Errorf("source.text 不是 flow.go 的原样子串")
	}

	// 不加 --include-source：原输出契约不变（无 source 键）
	code, out, errOut = run("query", "symbol", storeInsert, "--json", "--repo", fixture)
	if code != 0 {
		t.Fatalf("plain symbol exit = %d\n%s", code, errOut)
	}
	var plain map[string]any
	if err := json.Unmarshal([]byte(out), &plain); err != nil {
		t.Fatalf("plain JSON 不可解码: %v", err)
	}
	if _, ok := plain["source"]; ok {
		t.Errorf("未加 --include-source 时不应出现 source 键:\n%s", out)
	}

	// todo23 §6：struct 现已支持——返回声明原文与 kind（原“不支持种类”
	// 契约只保留给真正无法定位的声明）。
	code, out, errOut = run("query", "symbol", "symbol:go:example.com/flow:Store",
		"--include-source", "--json", "--repo", fixture)
	if code != 0 {
		t.Fatalf("struct --include-source exit = %d\n%s", code, errOut)
	}
	var structOut struct {
		Source struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"source"`
	}
	if err := json.Unmarshal([]byte(out), &structOut); err != nil {
		t.Fatalf("struct JSON 不可解码: %v\n%s", err, out)
	}
	if structOut.Source.Kind != "struct" || !strings.Contains(structOut.Source.Text, "type Store struct") {
		t.Errorf("struct source 应含 kind=struct 与声明原文: %+v", structOut.Source)
	}
}
