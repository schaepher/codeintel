//go:build integration

package integration

// todo22：`query sequence --format svg` 的真实渲染回归——同一张大时序图
// 用 PNG 会被 PlantUML 尺寸上限（默认 4096）裁切，SVG 则完整且不受该上限
// 约束。全链走真实 init → CLI → java/plantuml.jar。

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// writeSVGRepo 生成"入口调用 N 个叶子函数"的模块——图高随调用数线性增长，
// 稳定超过 PlantUML 默认 4096 像素上限。
func writeSVGRepo(t *testing.T, calls int) string {
	t.Helper()
	var flow strings.Builder
	flow.WriteString("package big\n\nfunc Entry() {\n")
	for i := 0; i < calls; i++ {
		flow.WriteString("\tStep")
		flow.WriteString(strconv.Itoa(i))
		flow.WriteString("()\n")
	}
	flow.WriteString("}\n\n")
	for i := 0; i < calls; i++ {
		flow.WriteString("func Step")
		flow.WriteString(strconv.Itoa(i))
		flow.WriteString("() {}\n")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/bigseq\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "big.go"), flow.String())
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// svgHeight 读 <svg height="…px"> 的高度（plantuml 输出为 px）。
func svgHeight(t *testing.T, svg string) int {
	t.Helper()
	m := regexp.MustCompile(`height="([0-9]+)px"`).FindStringSubmatch(svg)
	if m == nil {
		t.Fatalf("SVG 缺少 height 属性:\n%s", svg[:minInt(300, len(svg))])
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("SVG height 解析失败: %v", err)
	}
	return n
}

// TestTodo22SequenceSVGNotTruncated:同一张图 PNG 触顶被裁、SVG 完整。
func TestTodo22SequenceSVGNotTruncated(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	plantumlJarForTest(t) // 设 PLANTUML_JAR（不可用时 skip）
	t.Setenv("PLANTUML_LIMIT_SIZE", "")
	outDir := t.TempDir()
	dir := writeSVGRepo(t, 140)

	// PNG：默认上限 4096 → 高度触顶（图被裁），stderr 有可操作提示
	pngPath := filepath.Join(outDir, "seq.png")
	code, _, stderr := runCLIOutErr(t, "query", "sequence", "Entry", "--repo", dir,
		"--code", "--depth", "1", "--format", "plantuml", "--out", pngPath)
	if code != 0 {
		t.Fatalf("PNG 渲染 exit = %d\n%s", code, stderr)
	}
	_, pngH := pngWH(t, pngPath)
	if pngH != 4096 {
		t.Fatalf("PNG 应触顶 4096（被裁切），实为高度 %d", pngH)
	}
	for _, want := range []string{"尺寸上限 4096", "--format svg"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("PNG 裁切提示应含 %q:\n%s", want, stderr)
		}
	}

	// SVG：同一源码/参数 → 完整矢量图（高度 > 4096，末尾调用在场）
	svgPath := filepath.Join(outDir, "seq.svg")
	code, stdout, stderr := runCLIOutErr(t, "query", "sequence", "Entry", "--repo", dir,
		"--code", "--depth", "1", "--format", "svg", "--out", svgPath)
	if code != 0 {
		t.Fatalf("SVG 渲染 exit = %d\n%s", code, stderr)
	}
	if !strings.Contains(stdout, svgPath) {
		t.Errorf("stdout 应打印输出路径:\n%s", stdout)
	}
	if strings.Contains(stderr, "尺寸上限") {
		t.Errorf("SVG 不受 PNG 尺寸上限约束，不应提示裁切:\n%s", stderr)
	}
	data, err := os.ReadFile(svgPath)
	if err != nil {
		t.Fatalf("SVG 文件未生成: %v", err)
	}
	svg := string(data)
	if !strings.Contains(svg, "<svg") {
		t.Fatalf("产物不是 SVG:\n%s", svg[:minInt(200, len(svg))])
	}
	if h := svgHeight(t, svg); h <= 4096 {
		t.Errorf("SVG 高度应超过 PNG 上限（说明未裁切），实为 %d", h)
	}
	if !strings.Contains(svg, "Step139") {
		t.Errorf("SVG 应含最后一次调用 Step139（底部内容未丢）")
	}
	// 可编辑源文件仍在（与 PNG 产物同款 sidecar）
	if _, err := os.Stat(svgPath + ".puml"); err != nil {
		t.Errorf("SVG 旁应保存 .puml 源: %v", err)
	}

	// 极端 PLANTUML_LIMIT_SIZE 也不影响 SVG（该上限只作用于位图）
	t.Setenv("PLANTUML_LIMIT_SIZE", "100")
	tinySVG := filepath.Join(outDir, "tiny.svg")
	code, _, stderr = runCLIOutErr(t, "query", "sequence", "Entry", "--repo", dir,
		"--code", "--depth", "1", "--format", "svg", "--out", tinySVG)
	if code != 0 {
		t.Fatalf("LIMIT=100 SVG exit = %d\n%s", code, stderr)
	}
	if data, err := os.ReadFile(tinySVG); err != nil {
		t.Fatalf("SVG 未生成: %v", err)
	} else if h := svgHeight(t, string(data)); h <= 4096 {
		t.Errorf("SVG 不受 PLANTUML_LIMIT_SIZE 影响，实为高度 %d", h)
	}
}

// TestTodo22SequenceSVGStdoutContract：无 --out 时 SVG 文本直接进 stdout
// （可管道），且不混入 mermaid/base64；PNG 触顶的手动出路不再是必需。
func TestTodo22SequenceSVGStdoutContract(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	plantumlJarForTest(t)
	dir := writeSVGRepo(t, 3)
	code, stdout, stderr := runCLIOutErr(t, "query", "sequence", "Entry", "--repo", dir,
		"--code", "--depth", "1", "--format", "svg")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "<svg") {
		t.Errorf("stdout 应是 SVG 文本:\n%s", stdout[:minInt(200, len(stdout))])
	}
	if strings.Contains(stdout, "sequenceDiagram") || strings.Contains(stdout, "base64") {
		t.Errorf("SVG 输出不应混入 mermaid/base64")
	}
}
