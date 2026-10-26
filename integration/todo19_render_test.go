//go:build integration

package integration

// todo19-8：真实 PlantUML JAR：诊断注释转换 + PNG/侧车产物 + 语法校验。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func plantumlJarForTest(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java 不可用")
	}
	jar := os.Getenv("PLANTUML_JAR")
	if jar == "" {
		jar = "/opt/plantuml/plantuml.jar"
	}
	if _, err := os.Stat(jar); err != nil {
		t.Skip("plantuml.jar 不可用")
	}
	if out, err := exec.Command("java", "-jar", jar, "-version").CombinedOutput(); err != nil {
		t.Skipf("plantuml 不可运行: %v\n%s", err, out)
	}
	t.Setenv("PLANTUML_JAR", jar)
	return jar
}

func TestTodo19PlantumlRealRenderer(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	jar := plantumlJarForTest(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/render-comment\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(`package example

func Submit(region string) string {
	if region == "CN" {
		return Save()
	}
	return Save()
}
func Save() string { return "pending" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	// Mermaid 诊断注释存在
	code, m := runCLIOut(t, "query", "sequence", "Submit", "--repo", dir,
		"--code", "--depth", "1", "--format", "mermaid")
	if code != 0 || !strings.Contains(m, "sequenceDiagram") ||
		!strings.Contains(m, "%% diag") {
		t.Fatalf("mermaid 应含诊断注释: code=%d\n%s", code, m)
	}
	outDir := t.TempDir()
	cases := [][]string{
		{"--depth", "1"},
		{"--depth", "2", "--focus", "return", "--scene", "region=CN", "--evidence"},
	}
	for i, extra := range cases {
		png := filepath.Join(outDir, "result-" + string(rune('a'+i)) + ".png")
		args := append([]string{"query", "sequence", "Submit", "--repo", dir, "--code",
			"--format", "plantuml", "--out", png}, extra...)
		code, stdout := runCLIOut(t, args...)
		if code != 0 {
			t.Fatalf("plantuml case %d exit=%d\n%s", i, code, stdout)
		}
		data, err := os.ReadFile(png)
		if err != nil {
			t.Fatalf("case %d 未生成 PNG: %v", i, err)
		}
		if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Errorf("case %d 不是 PNG（前缀 %q）", i, string(data[:minInt(8, len(data))]))
		}
		sidecar := strings.TrimSuffix(png, ".png") + ".puml"
		puml, err := os.ReadFile(sidecar)
		if err != nil {
			t.Fatalf("case %d 缺少 .puml sidecar: %v", i, err)
		}
		ps := string(puml)
		if strings.Contains(ps, "%%") {
			t.Errorf("case %d puml 仍含 Mermaid 注释:\n%s", i, ps)
		}
		if !strings.Contains(ps, "' diag") {
			t.Errorf("case %d 诊断应转为 PlantUML 注释:\n%s", i, ps)
		}
		if out, err := exec.Command("java", "-jar", jar, "-checkonly", sidecar).CombinedOutput(); err != nil {
			t.Errorf("case %d PlantUML 语法校验失败: %v\n%s\n%s", i, err, out, ps)
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
