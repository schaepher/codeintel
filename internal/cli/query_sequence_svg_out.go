package cli

// query sequence 的 SVG 出口与 --out 写入辅助（todo22）+ 预算标注。
// 从 query_sequence_code.go 拆出（行数治理）。

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"
)

// plantumlRenderSVGFunc SVG 渲染（可在测试中注入——同 plantumlRenderFunc）。
var plantumlRenderSVGFunc = plantumlRenderSVG

// renderCodeSeqSVG mermaid → PlantUML → SVG 字节（todo22）。
//
// 与 PNG 出口的差别是**失败语义**：显式请求矢量图时渲染失败即非零退出，
// 不降级写 mermaid 文本、不把文本写进 .svg（"看起来成功"的假产物比失败
// 更难发现）。成功时与 PNG 一致：--out 写文件 + 同名 .puml 可编辑源；
// 无 --out 则把 SVG 文本打到 stdout（可管道/diff）。
func renderCodeSeqSVG(m, out string) (int, bool) {
	puml := mermaidSequenceToPlantuml(m)
	if puml == "" {
		fmt.Fprintln(os.Stderr, "error: PlantUML 转换结果为空（未生成 SVG）")
		return 1, false
	}
	svg, err := plantumlRenderSVGFunc(puml)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: SVG 渲染失败: %v\n", err)
		return 1, false
	}
	if len(svg) == 0 || !bytes.Contains(svg, []byte("<svg")) {
		fmt.Fprintln(os.Stderr, "error: SVG 渲染结果无效（输出中没有 <svg>）")
		return 1, false
	}
	if out != "" {
		if !writeOutFile(out, svg) {
			return 1, true
		}
		if !writeOutFile(pumlSidecarPath(out), []byte(puml)) {
			return 1, true
		}
		return 0, true
	}
	fmt.Print(string(svg))
	if !bytes.HasSuffix(svg, []byte("\n")) {
		fmt.Println()
	}
	return 0, false
}

// seqBudgetLabel 查询预算的可读标注（未设置预算时 "none"）。
func seqBudgetLabel(timeout time.Duration) string {
	if timeout <= 0 {
		return "none"
	}
	return timeout.String()
}

// seqTimingEnabled CODEINTEL_SEQ_TIMING 是否开启。
func seqTimingEnabled() bool {
	v := strings.TrimSpace(os.Getenv(seqTimingEnv))
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// writeOutFile --out 写文件（成功打印路径；失败 stderr 提示并返回 false）。
func writeOutFile(out string, data []byte) bool {
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: 写入 %s 失败: %v\n", out, err)
		return false
	}
	fmt.Printf("已写入 %s\n", out)
	return true
}

// pumlSidecarPath PNG 产物旁的 PlantUML 源文件路径（可编辑）。.png → .puml；
// 其余后缀直接追加 .puml。
func pumlSidecarPath(out string) string {
	if strings.HasSuffix(strings.ToLower(out), ".png") {
		return out[:len(out)-4] + ".puml"
	}
	return out + ".puml"
}
