//go:build integration

package integration

// todo20-8/10：PlantUML 默认 headless 启动与尺寸上限提示（真实 init → CLI）。

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func writeTodo20Repo(t *testing.T, flow string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"),
		"module example.com/render-comment\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "flow.go"), flow)
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

const todo20Flow = `package example

func Submit(region string) string {
	if region == "CN" {
		return Save()
	}
	return Save()
}

func Save() string { return "pending" }
`

// pngWH 读 PNG IHDR 宽高。
func pngWH(t *testing.T, path string) (int, int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 PNG %s: %v", path, err)
	}
	if len(b) < 24 || string(b[:8]) != "\x89PNG\r\n\x1a\n" || string(b[12:16]) != "IHDR" {
		t.Fatalf("%s 不是 PNG", path)
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24]))
}

// unsetJavaToolOptions 去掉调用方偶然设置的 headless（默认路径必须自己
// 保证启动模式）。
func unsetJavaToolOptions(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("JAVA_TOOL_OPTIONS")
	if err := os.Unsetenv("JAVA_TOOL_OPTIONS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv("JAVA_TOOL_OPTIONS", old)
		} else {
			os.Unsetenv("JAVA_TOOL_OPTIONS")
		}
	})
}

func TestTodo20PlantumlDefaultHeadless(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	jar := plantumlJarForTest(t)
	dir := writeTodo20Repo(t, todo20Flow)
	tmp := filepath.Join(t.TempDir(), "render-tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLANTUML_TMPDIR", tmp)
	unsetJavaToolOptions(t)
	javaOptions := os.Getenv("JAVA_TOOL_OPTIONS") // 必须是空（默认路径自证）

	outDir := t.TempDir()
	cases := [][]string{
		{"--depth", "1"},
		{"--depth", "2", "--focus", "return", "--scene", "region=CN", "--evidence"},
	}
	for i, extra := range cases {
		png := filepath.Join(outDir, "result-"+strconv.Itoa(i)+".png")
		args := append([]string{"query", "sequence", "Submit", "--repo", dir, "--code",
			"--format", "plantuml", "--out", png}, extra...)
		code, stdout, stderr := runCLIOutErr(t, args...)
		if code != 0 {
			t.Fatalf("case %d exit=%d stdout=%s stderr=%s", i, code, stdout, stderr)
		}
		if javaOptions != "" {
			t.Fatalf("测试前提：调用方未设置 JAVA_TOOL_OPTIONS，实为 %q", javaOptions)
		}
		if _, h := pngWH(t, png); h == 0 {
			t.Fatalf("case %d PNG 无高度", i)
		}
		sidecar := strings.TrimSuffix(png, ".png") + ".puml"
		puml, err := os.ReadFile(sidecar)
		if err != nil {
			t.Fatalf("case %d 缺少 .puml sidecar: %v", i, err)
		}
		ps := string(puml)
		for _, want := range []string{"@startuml", "' diag", "alt region == \"CN\"",
			"P0->>P1: Save", "P0-->>P0: return Save()"} {
			if !strings.Contains(ps, want) {
				t.Errorf("case %d puml 应含 %q:\n%s", i, want, ps)
			}
		}
		if out, err := exec.Command("java", "-Djava.awt.headless=true", "-jar", jar,
			"-checkonly", sidecar).CombinedOutput(); err != nil {
			t.Errorf("case %d PlantUML 语法校验失败: %v\n%s\n%s", i, err, out, ps)
		}
		if strings.Contains(stderr, "渲染超时") || strings.Contains(stderr, "plantuml 渲染失败") {
			t.Errorf("case %d 默认 headless 不应失败: %s", i, stderr)
		}
	}
}

func TestTodo20PlantumlSizeLimitWarning(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	jar := plantumlJarForTest(t)
	var src strings.Builder
	src.WriteString("package example\n\nfunc Submit() string {\n")
	for i := 0; i < 100; i++ {
		src.WriteString("    Step()\n")
	}
	src.WriteString("    return \"done\"\n}\n\nfunc Step() {}\n")
	dir := writeTodo20Repo(t, src.String())
	tmp := filepath.Join(t.TempDir(), "render-tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLANTUML_TMPDIR", tmp)
	t.Setenv("JAVA_TOOL_OPTIONS", "-Djava.awt.headless=true")
	outDir := t.TempDir()

	// 默认上限：PNG 触顶 → 必须提示可能裁切及扩大方法。
	os.Unsetenv("PLANTUML_LIMIT_SIZE")
	defPNG := filepath.Join(outDir, "default.png")
	code, _, stderr := runCLIOutErr(t, "query", "sequence", "Submit", "--repo", dir,
		"--code", "--depth", "1", "--format", "plantuml", "--out", defPNG)
	if code != 0 {
		t.Fatalf("default exit=%d stderr=%s", code, stderr)
	}
	_, defH := pngWH(t, defPNG)
	if defH != 4096 {
		t.Fatalf("默认上限 PNG 高度应为 4096，实为 %d", defH)
	}
	for _, want := range []string{"尺寸上限 4096", "可能被裁切", "PLANTUML_LIMIT_SIZE"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("默认大图应有裁切提示 %q，stderr=%s", want, stderr)
		}
	}

	// 显式放大上限：完整 PNG（与同 .puml 的 SVG 自然范围对照）。
	t.Setenv("PLANTUML_LIMIT_SIZE", "16384")
	fullPNG := filepath.Join(outDir, "full.png")
	code, _, stderr = runCLIOutErr(t, "query", "sequence", "Submit", "--repo", dir,
		"--code", "--depth", "1", "--format", "plantuml", "--out", fullPNG)
	if code != 0 {
		t.Fatalf("full exit=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "尺寸上限") {
		t.Errorf("放大上限后不应再报裁切: %s", stderr)
	}
	fullW, fullH := pngWH(t, fullPNG)
	if fullH <= 4096 {
		t.Fatalf("放大上限后 PNG 应完整（>4096），实为 %dx%d", fullW, fullH)
	}
	fullPuml := strings.TrimSuffix(fullPNG, ".png") + ".puml"
	svgPath := filepath.Join(outDir, "full.svg")
	svgCmd := exec.Command("java", "-Djava.awt.headless=true", "-jar", jar,
		"-tsvg", "-o", outDir, fullPuml)
	if out, err := svgCmd.CombinedOutput(); err != nil {
		t.Fatalf("SVG 对照失败: %v\n%s", err, out)
	}
	svg, err := os.ReadFile(svgPath)
	if err != nil {
		t.Fatalf("SVG 对照文件: %v", err)
	}
	sv := string(svg)
	if !strings.Contains(sv, `return &quot;done&quot;`) && !strings.Contains(sv, `return "done"`) {
		t.Errorf("SVG 应包含最后一条 return 消息:\n%s", sv[:minInt(500, len(sv))])
	}
	if got := strings.Count(sv, ">Step<"); got < 100 {
		t.Errorf("SVG 应包含 100 个 Step 消息/参与者，实为 %d", got)
	}
	m := regexp.MustCompile(`height="([0-9]+)px"`).FindStringSubmatch(sv)
	if m == nil {
		t.Fatalf("SVG 无 height")
	}
	svgH, _ := strconv.Atoi(m[1])
	if fullH < svgH-20 || fullH > svgH+20 {
		t.Errorf("完整 PNG 高度 %d 应与 SVG 自然高度 %d 接近（±20）", fullH, svgH)
	}
	if defH >= svgH-100 {
		t.Errorf("默认 PNG 高度 %d 应小于完整 SVG 高度 %d（确有裁切）", defH, svgH)
	}
	// 最后消息/底部参与者在 .puml 源里也在（PNG 只是其位图出口）。
	puml, err := os.ReadFile(fullPuml)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(puml), "P0->>P1: Step") != 100 ||
		!strings.Contains(string(puml), `P0-->>P0: return "done"`) {
		t.Errorf("完整图 .puml 应含 100 次 Step 与最后 return done")
	}
}
