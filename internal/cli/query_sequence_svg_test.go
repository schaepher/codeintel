package cli

// todo22：`query sequence --format svg`——PlantUML 矢量图出口。
//
// 动机：PNG 受 PlantUML 默认 PLANTUML_LIMIT_SIZE=4096 限制，大时序图会被
// 裁切（同一图 300 步 PNG 238×4096，SVG 239×9655 且内容完整）。本回归锁定
// 契约：显式 --format svg → 输出 SVG；失败即失败（不降级写文本）；扩展名
// 与内容必须一致（plantuml 模式写 .svg 直接报错——历史上会把 PNG 字节写进
// .svg 且 exit=0）。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSVGRender 替换 SVG 渲染器并返回被调用的 puml。
func stubSVGRender(t *testing.T, out []byte, err error) *string {
	t.Helper()
	var got string
	old := plantumlRenderSVGFunc
	plantumlRenderSVGFunc = func(p string) ([]byte, error) { got = p; return out, err }
	t.Cleanup(func() { plantumlRenderSVGFunc = old })
	return &got
}

// TestCmdSeqSVGStdout：--format svg 无 --out → stdout 是 SVG 文本（不是 base64）。
func TestCmdSeqSVGStdout(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	got := stubSVGRender(t, []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), nil)
	var code int
	stdout := captureStdout(func() {
		code = cmdQuery([]string{"sequence", "--code", "Prepare", "--repo", dir, "--format", "svg"})
	})
	if code != 0 {
		t.Fatalf("--format svg exit = %d", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "<svg") {
		t.Errorf("stdout 应是 SVG 文本:\n%s", stdout)
	}
	if strings.Contains(stdout, "base64") || strings.Contains(stdout, "sequenceDiagram") {
		t.Errorf("SVG 输出不应混入 base64/mermaid:\n%s", stdout)
	}
	if !strings.Contains(*got, "@startuml") || !strings.Contains(*got, "@enduml") {
		t.Errorf("应把 mermaid 转成 PlantUML 再渲染:\n%s", *got)
	}
}

// TestCmdSeqSVGOutFile：--format svg --out x.svg → 写 SVG + 同名 .puml 源，
// stdout 打印路径。
func TestCmdSeqSVGOutFile(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	svgBytes := []byte(`<svg xmlns="http://www.w3.org/2000/svg" height="9655px"></svg>`)
	got := stubSVGRender(t, svgBytes, nil)
	outFile := filepath.Join(t.TempDir(), "seq.svg")
	var code int
	stdout := captureStdout(func() {
		code = cmdQuery([]string{"sequence", "--code", "Prepare", "--repo", dir,
			"--format", "svg", "--out", outFile})
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("SVG 文件未生成: %v", err)
	}
	if string(data) != string(svgBytes) {
		t.Errorf("SVG 文件内容 = %q", string(data))
	}
	side, err := os.ReadFile(outFile + ".puml")
	if err != nil {
		t.Fatalf("应保存可编辑的 PlantUML 源: %v", err)
	}
	if string(side) != *got {
		t.Errorf("PlantUML 源 = %q; want %q", string(side), *got)
	}
	if !strings.Contains(stdout, outFile) {
		t.Errorf("stdout 应打印输出路径:\n%s", stdout)
	}
}

// TestCmdSeqSVGFailureDoesNotWrite：显式请求 SVG 时渲染失败 → 非零退出，
// 不写文件、不降级文本（与 PNG 的 fallback 语义不同）。
func TestCmdSeqSVGFailureDoesNotWrite(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	stubSVGRender(t, nil, errors.New("jar 不可用"))
	outFile := filepath.Join(t.TempDir(), "seq.svg")
	var code int
	stdout := captureStdout(func() {
		stderr := captureStderr(func() {
			code = cmdQuery([]string{"sequence", "--code", "Prepare", "--repo", dir,
				"--format", "svg", "--out", outFile})
		})
		if !strings.Contains(stderr, "SVG 渲染失败") || !strings.Contains(stderr, "jar 不可用") {
			t.Errorf("stderr 应说明 SVG 渲染失败原因:\n%s", stderr)
		}
	})
	if code == 0 {
		t.Fatalf("SVG 渲染失败必须非零退出")
	}
	if _, err := os.Stat(outFile); !os.IsNotExist(err) {
		t.Errorf("失败时不得写出 %s（不降级文本、不留假产物）", outFile)
	}
	if stdout != "" {
		t.Errorf("失败时 stdout 应为空:\n%s", stdout)
	}
}

// TestCmdSeqSVGRejectsMismatchedExtensions：
//   - plantuml/mermaid/文本模式 + --out *.svg → 报错（历史上 PNG 字节被写进 .svg）
//   - svg 模式 + --out *.png → 报错（内容是 SVG 文本，不是 PNG）
//   - svg 模式 + --json → 报错（两种输出契约互斥）
func TestCmdSeqSVGRejectsMismatchedExtensions(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	got := stubSVGRender(t, []byte("<svg/>"), nil)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"plantuml→.svg", []string{"--format", "plantuml", "--out", filepath.Join(t.TempDir(), "x.svg")}, "--format svg"},
		{"mermaid→.svg", []string{"--format", "mermaid", "--out", filepath.Join(t.TempDir(), "y.svg")}, "--format svg"},
		{"text→.svg", []string{"--out", filepath.Join(t.TempDir(), "z.svg")}, "--format svg"},
		{"svg→.png", []string{"--format", "svg", "--out", filepath.Join(t.TempDir(), "x.png")}, ".png"},
		{"svg+json", []string{"--format", "svg", "--json"}, "--json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var code int
			args := append([]string{"sequence", "--code", "Prepare", "--repo", dir}, c.args...)
			stderr := captureStderr(func() {
				captureStdout(func() { code = cmdQuery(args) })
			})
			if code == 0 {
				t.Fatalf("%s 应报错退出（args=%v）", c.name, c.args)
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("stderr 应提示 %q:\n%s", c.want, stderr)
			}
			for _, a := range c.args {
				if strings.HasSuffix(a, "/") {
					continue
				}
				if strings.HasSuffix(a, ".svg") || strings.HasSuffix(a, ".png") {
					if _, err := os.Stat(a); !os.IsNotExist(err) {
						t.Errorf("拒绝时不应写文件 %s", a)
					}
				}
			}
		})
	}
	if *got != "" {
		t.Errorf("扩展名/契约校验失败时不应进入渲染: %q", *got)
	}
}

// TestPlantumlJavaArgsSVG：SVG 渲染必须用 -tsvg（不是 -tpng），其余启动
// 参数（headless/tmpdir/-pipe）与 PNG 路径一致。
func TestPlantumlJavaArgsSVG(t *testing.T) {
	svgArgs := strings.Join(plantumlJavaArgsFormat("/tmp/plantuml.jar", "svg"), " ")
	for _, want := range []string{"-Djava.awt.headless=true", "-Djava.io.tmpdir=", "-tsvg", "-pipe"} {
		if !strings.Contains(svgArgs, want) {
			t.Errorf("SVG 启动参数应含 %q: %s", want, svgArgs)
		}
	}
	if strings.Contains(svgArgs, "-tpng") {
		t.Errorf("SVG 参数不应含 -tpng: %s", svgArgs)
	}
	if pngArgs := strings.Join(plantumlJavaArgs("/tmp/plantuml.jar"), " "); !strings.Contains(pngArgs, "-tpng") {
		t.Errorf("PNG 参数应保持 -tpng: %s", pngArgs)
	}
}

// TestPlantumlSizeWarningSuggestsSVG：PNG 触顶提示必须给出 CLI 内的
// 矢量图出口（--format svg），而不是只让用户手敲 java 命令。
func TestPlantumlSizeWarningSuggestsSVG(t *testing.T) {
	t.Setenv("PLANTUML_LIMIT_SIZE", "")
	w := plantumlSizeWarning(fakePNG(500, 4096))
	if !strings.Contains(w, "--format svg") {
		t.Errorf("裁切提示应指向 --format svg: %q", w)
	}
}
