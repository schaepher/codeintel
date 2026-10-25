package cli

// R81 `query sequence --code <符号>`——代码级时序图（源码 AST 转时序）。
// R95：查询逻辑迁 action（Actions.CodeSequence）；cli 只做参数解析
// （--depth/停止包配置加载）与输出渲染（mermaid/缩进文本/JSON）。
// R100：--out <file>——输出写文件（plantuml → PNG；渲染失败 fallback
// 写 mermaid 文本；mermaid/json/文本同样支持），stdout 打印路径。

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"go.uber.org/zap"
)

// cmdQuerySequenceCode 实现 `query sequence --code <符号> [--depth N] [--format mermaid|plantuml] [--json] [--out <file>]`。
// depth：嵌套层级（1 = 只本函数；>1 递归展开被调函数内部）。
// diagram：plantuml 时 mermaid → plantuml 转 PNG base64 输出；失败
// 写文本（S4——用户：配置 plantuml.jar 用它转图片，失败才写文本）。
// out：非空时主输出写文件（plantuml 成功 → PNG 字节；否则文本），
// stdout 打印路径。
func cmdQuerySequenceCode(acts *action.Actions, abs, target string, depth int, mermaid bool, jsonOut bool, diagram, out string) int {
	if depth <= 0 {
		depth = 1
	}
	root, err := acts.CodeSequence(action.CodeSequenceRequest{
		Target: target, RepoAbs: abs, Depth: depth, StopPackages: loadSeqStopPkgs(),
		Filter: loadSeqFilter()})
	if err != nil {
		// todo10-9：主动终止（取消）不得报告为成功
		if errors.Is(err, action.ErrSeqCanceled) {
			fmt.Fprintln(os.Stderr, "error: 代码级时序展开已取消（未完成的分析不作为结果输出）")
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if root == nil {
		fmt.Fprintf(os.Stderr, "error: 代码级时序不可用（符号解析失败或源码缺失——可尝试不带 --code 用索引调用链）\n")
		return 1
	}
	// 诊断开关与"是否进 JSON"在渲染前决定（JSON 一旦编码就定型）
	timing := root.Meta.Timing
	enabled := seqTimingEnabled()
	if !enabled && root.Meta != nil {
		root.Meta.Timing = nil // 默认输出不含诊断字段（保持既有契约）
	}
	// 渲染阶段（todo10-9：含 mermaid/plantuml 转换、写文件）
	renderStart := time.Now()
	code, _ := renderCodeSeq(root, mermaid, jsonOut, diagram, out)
	finishSeqTiming(timing, jsonOut, renderStart, enabled)
	return code
}

// renderCodeSeq 把时序树渲染成目标格式并输出（wrote=true 表示已写文件，
// 不再额外打印内容）。渲染失败返回非零退出码。
func renderCodeSeq(root *action.CodeSeqNode, mermaid, jsonOut bool, diagram, out string) (code int, wrote bool) {
	if jsonOut {
		var data []byte
		if out != "" {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetIndent("", "  ")
			if err := enc.Encode(root); err != nil {
				fmt.Fprintf(os.Stderr, "error: 编码 JSON 失败: %v\n", err)
				return 1, false
			}
			data = buf.Bytes()
		}
		if out != "" {
			if !writeOutFile(out, data) {
				return 1, true
			}
			return 0, true
		}
		encodeJSON(root)
		return 0, false
	}
	if mermaid {
		m := renderCodeSeqMermaid(root)
		if diagram == "plantuml" {
			if puml := mermaidSequenceToPlantuml(m); puml != "" {
				if png, err := plantumlRenderFunc(puml); err == nil {
					if out != "" {
						if !writeOutFile(out, png) {
							return 1, true
						}
						// R17：PNG 旁保存可编辑的 PlantUML 源文件（供编辑器打开编辑，
						// 不再把 PNG 交给文本编辑器）。
						if !writeOutFile(pumlSidecarPath(out), []byte(puml)) {
							return 1, true
						}
						return 0, true
					}
					fmt.Println("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
					return 0, false
				}
			}
			// 转换/渲染失败 → 写文本（mermaid 原文）
		}
		if out != "" {
			if !writeOutFile(out, []byte(m)) {
				return 1, true
			}
			return 0, true
		}
		fmt.Print(m)
		return 0, false
	}
	// 默认文本：缩进树（源码顺序 + 分支/循环标注）
	text := fmt.Sprintf("== %s 代码级时序 ==\n", root.Label) + seqText(root.Nodes, 1)
	if out != "" {
		if !writeOutFile(out, []byte(text)) {
			return 1, true
		}
		return 0, true
	}
	fmt.Print(text)
	return 0, false
}

// seqTimingEnv 开启代码级时序阶段诊断的环境变量（todo10-9）。
const seqTimingEnv = "CODEINTEL_SEQ_TIMING"

// finishSeqTiming 填入渲染耗时，并按开关输出诊断（todo10-9）：
//   - 始终写结构化日志（logger.Debug 级，进 .codeintel/codeintel.log）
//   - CODEINTEL_SEQ_TIMING=1 时额外往 stderr 打一行（stdout 的 JSON 契约不变）
//   - 开关关闭时从 JSON 移除 timing（默认输出保持不变）
func finishSeqTiming(timing *action.SeqTiming, jsonOut bool, renderStart time.Time, enabled bool) {
	if timing == nil {
		return
	}
	// JSON 输出：编码即输出（自指）——render_ms 记 0，total 不含编码耗时
	if !jsonOut {
		timing.RenderMS = float64(time.Since(renderStart).Microseconds()) / 1000
		timing.TotalMS += timing.RenderMS
	}
	zap.L().Debug("seq render timing",
		zap.Int("nodes", timing.Nodes), zap.Float64("render_ms", timing.RenderMS),
		zap.Float64("total_ms", timing.TotalMS), zap.String("stop", timing.Stop))
	if !enabled {
		return
	}
	fmt.Fprintf(os.Stderr, "[seq] resolve=%.3fms read=%.3fms walk=%.3fms render=%.3fms total=%.3fms nodes=%d depth=%d stop=%s\n",
		timing.ResolveMS, timing.ReadMS, timing.WalkMS, timing.RenderMS, timing.TotalMS,
		timing.Nodes, timing.Depth, timing.Stop)
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
