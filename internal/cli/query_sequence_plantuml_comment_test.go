package cli

// todo19-8：Mermaid 诊断/证据注释 → PlantUML 注释；PNG 渲染失败不静默写文本。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMermaidCommentsConvertedToPlantuml(t *testing.T) {
	m := "sequenceDiagram\n" +
		"%% diag depth x1 depth 上限（--depth 1）\n" +
		"%% diag typecheck_errors x2 bad.go:3: cannot use X\n" +
		"%% evidence P0->P1 @ flow.go:3 id=symbol:go:example.com/m:Save verdict=direct\n" +
		"  participant P0 as Submit\n" +
		"  P0->>P1: Save\n"
	p := mermaidSequenceToPlantuml(m)
	for _, line := range strings.Split(p, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "%%") {
			t.Errorf("PlantUML 输出不应含 Mermaid 注释: %q", line)
		}
	}
	if !strings.Contains(p, "' diag depth x1 depth 上限") ||
		!strings.Contains(p, "' evidence P0->P1 @ flow.go:3") {
		t.Errorf("诊断/证据应转为 PlantUML 注释:\n%s", p)
	}
	// 注释内容不得含裸换行（多行 detail 已压平）
	for _, l := range strings.Split(p, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "'") && strings.Contains(l, "\r") {
			t.Errorf("注释含回车: %q", l)
		}
	}
}

func TestPlantumlPngFailureDoesNotWriteTextToPng(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	old := plantumlRenderFunc
	plantumlRenderFunc = func(string) ([]byte, error) { return nil, errors.New("jar 不可用") }
	defer func() { plantumlRenderFunc = old }()
	outDir := t.TempDir()
	png := filepath.Join(outDir, "result.png")
	var code int
	stderr := captureStderr(func() {
		captureStdout(func() {
			code = cmdQuery([]string{"sequence", "--code", "Prepare", "--repo", dir,
				"--format", "plantuml", "--out", png})
		})
	})
	if code == 0 {
		t.Errorf("PNG 渲染失败应非零退出（调用者可区分）")
	}
	if _, err := os.Stat(png); err == nil {
		t.Errorf("失败时不得生成/写入 result.png")
	}
	puml := filepath.Join(outDir, "result.puml")
	data, err := os.ReadFile(puml)
	if err != nil {
		t.Fatalf("应写文本回退 .puml: %v\n%s", err, stderr)
	}
	if !strings.Contains(string(data), "@startuml") {
		t.Errorf("回退应为 PlantUML 源:\n%s", data)
	}
	if !strings.Contains(stderr, "plantuml 渲染失败") || !strings.Contains(stderr, "未生成 PNG") {
		t.Errorf("应明确失败原因与未生成 PNG: %s", stderr)
	}
}
