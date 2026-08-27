package cli

// R100 待办8：sequence plantuml --out 文件输出（--format plantuml 现
// 输出 base64 一行——加 --out 保存 PNG 文件；渲染失败 fallback 写
// mermaid 文本，不丢输出）。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCmdSeqPlantumlOut：--format plantuml --out <file> → PNG 写入文件，
// stdout 打印路径（不再输出 base64 一行）。
func TestCmdSeqPlantumlOut(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	old := plantumlRenderFunc
	plantumlRenderFunc = func(string) ([]byte, error) { return []byte("FAKE-PNG"), nil }
	defer func() { plantumlRenderFunc = old }()
	outFile := filepath.Join(t.TempDir(), "seq.png")
	stdout := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "--code", "Prepare", "--repo", dir,
			"--format", "plantuml", "--out", outFile}); code != 0 {
			t.Fatalf("sequence --code --out exit = %d", code)
		}
	})
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "FAKE-PNG" {
		t.Errorf("PNG 文件内容 = %q; want FAKE-PNG", string(data))
	}
	if !strings.Contains(stdout, outFile) {
		t.Errorf("stdout 应打印输出路径:\n%s", stdout)
	}
	if strings.Contains(stdout, "base64") {
		t.Errorf("--out 不应再输出 base64 行:\n%s", stdout)
	}
}

// TestCmdSeqPlantumlOutFallback：jar 不可用（渲染失败）→ fallback 写
// mermaid 文本到 out（输出不丢）。
func TestCmdSeqPlantumlOutFallback(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	old := plantumlRenderFunc
	plantumlRenderFunc = func(string) ([]byte, error) { return nil, errors.New("jar 不可用") }
	defer func() { plantumlRenderFunc = old }()
	outFile := filepath.Join(t.TempDir(), "seq.txt")
	stdout := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "--code", "Prepare", "--repo", dir,
			"--format", "plantuml", "--out", outFile}); code != 0 {
			t.Fatalf("sequence --code --out(失败回退) exit = %d", code)
		}
	})
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sequenceDiagram") {
		t.Errorf("渲染失败应 fallback 写 mermaid 文本:\n%s", string(data))
	}
	if !strings.Contains(stdout, outFile) {
		t.Errorf("stdout 应打印输出路径:\n%s", stdout)
	}
}

// TestCmdSeqOutMermaid：--format mermaid --out → 写 mermaid 文本文件。
func TestCmdSeqOutMermaid(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	outFile := filepath.Join(t.TempDir(), "seq.mmd")
	captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "--code", "Prepare", "--repo", dir,
			"--format", "mermaid", "--out", outFile}); code != 0 {
			t.Fatalf("sequence --code --out(mermaid) exit = %d", code)
		}
	})
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sequenceDiagram") {
		t.Errorf("mermaid --out 应写文本文件:\n%s", string(data))
	}
}
