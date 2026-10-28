package cli

// R81 `query sequence --code <符号>`——代码级时序图（源码 AST 转时序）。
// R95：查询逻辑迁 action（Actions.CodeSequence）；cli 只做参数解析
// （--depth/停止包配置加载）与输出渲染（mermaid/缩进文本/JSON）。
// R100：--out <file>——输出写文件（plantuml → PNG；渲染失败 fallback
// 写 mermaid 文本；mermaid/json/文本同样支持），stdout 打印路径。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"go.uber.org/zap"
)

// cmdQuerySequenceCode 实现 `query sequence --code <符号> [--depth N]
// [--format mermaid|plantuml|svg] [--json] [--out <file>] [--focus <字段|return>]`。
// depth：嵌套层级（1 = 只本函数；>1 递归展开被调函数内部）。
// diagram：plantuml 时 mermaid → plantuml 转 PNG base64 输出；失败
// 写文本（S4——用户：配置 plantuml.jar 用它转图片，失败才写文本）。
// svg（todo22）时走同一转换但用 -tsvg 渲染矢量图：PNG 受 PlantUML 默认
// PLANTUML_LIMIT_SIZE=4096 限制会被裁切，SVG 不受限；显式要矢量图时
// **失败即失败**（不降级文本、不写假扩展名）。
// out：非空时主输出写文件（plantuml 成功 → PNG 字节；svg → SVG 文本；
// 否则文本），stdout 打印路径。扩展名与内容必须一致：非 svg 模式写
// *.svg、svg 模式写 *.png 都直接报错。
func cmdQuerySequenceCode(acts *action.Actions, abs, target string, depth, contSegments int, mermaid bool, jsonOut bool, diagram, out string, focus []string, evidence bool, scene []string, timeout time.Duration, verifyEvidence string, allRepos bool) int {
	if depth <= 0 {
		depth = 1
	}
	// todo22：输出契约校验要在**查询之前**完成——错误扩展名不得先跑一遍
	// 大图分析再失败；更重要的是不能把 PNG 字节写进 .svg（历史上如此，
	// exit=0，用 file 才能发现）。
	if jsonOut && diagram == "svg" {
		fmt.Fprintln(os.Stderr, "error: --json 与 --format svg 互斥（前者输出 JSON，后者输出矢量图）")
		return 1
	}
	if out != "" && strings.EqualFold(filepath.Ext(out), ".svg") && diagram != "svg" {
		fmt.Fprintf(os.Stderr, "error: --out %s 是 .svg，但 --format %s 不产出 SVG；请用 --format svg（或改用 .png/.mmd/文本扩展名）\n",
			out, diagram)
		return 1
	}
	if diagram == "svg" && out != "" && strings.EqualFold(filepath.Ext(out), ".png") {
		fmt.Fprintf(os.Stderr, "error: --format svg 的内容是 SVG 文本，不能写到 .png（%s）；请改用 .svg\n", out)
		return 1
	}
	// todo21-14.5：预算口径起点——创建查询 ctx **之前**（含请求配置构造），
	// 终点是 CodeSequence 同步返回。此前的仓库打开/新鲜度检查与之后的
	// 渲染都不计入。
	queryStart := time.Now()
	ctx := context.Background()
	if timeout > 0 {
		// todo12-17 R3：查询预算——超时取消展开，失败退出（不返回看似完整的成功）
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// todo22 §4.1：RPC 跨仓续接候选（默认按同一 workspace 限定，--all-repos
	// 显式扩展；候选范围与 ext-chain 同一口径）。
	cands, scope, scopeReason := seqCandidateRepos(abs, allRepos)
	req := action.CodeSequenceRequest{
		Target: target, RepoAbs: abs, Depth: depth, ContinueSegments: contSegments,
		StopPackages: loadSeqStopPkgs(),
		Filter: loadSeqFilter(), Focus: focus, Scene: action.NewScene(scene), Ctx: ctx,
		Evidence: evidence || verifyEvidence != "", Format: diagram, Out: out,
		CandidateRepos: cands, Open: openRepoActions, Scope: scope, ScopeReason: scopeReason}
	req.SetCLIVersion(version())
	root, err := acts.CodeSequence(req)
	// todo21-14.5：无论成功/失败都留一条可断言的预算范围记录（仅诊断开关
	// 打开时写 stderr——默认 stdout/JSON 契约不变）。
	if seqTimingEnabled() {
		canceled := errors.Is(err, action.ErrSeqCanceled) || ctx.Err() != nil
		fmt.Fprintf(os.Stderr, "[seq.query] elapsed=%.3fms budget=%s canceled=%t\n",
			float64(time.Since(queryStart).Microseconds())/1000, seqBudgetLabel(timeout), canceled)
	}
	if err != nil {
		// todo10-9：主动终止（取消）不得报告为成功
		if errors.Is(err, action.ErrSeqCanceled) {
			if timeout > 0 && ctx.Err() == context.DeadlineExceeded {
				fmt.Fprintf(os.Stderr, "error: 代码级时序超过查询预算 %s——已停止后台展开，结果不完整不输出\n", timeout)
			} else {
				fmt.Fprintln(os.Stderr, "error: 代码级时序展开已取消（未完成的分析不作为结果输出）")
			}
			return 1
		}
		var diag *action.SeqDiagError
		if errors.As(err, &diag) {
			fmt.Fprintf(os.Stderr, "error: %v\n", diag)
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
	code, _ := renderCodeSeq(root, mermaid, jsonOut, diagram, out, evidence)
	finishSeqTiming(timing, jsonOut, renderStart, enabled)
	// todo13-14：验证原证据身份（执行新分析 ≠ 复现同一次分析）
	if verifyEvidence != "" {
		if vcode := verifySeqEvidence(root, verifyEvidence); vcode != 0 {
			return vcode
		}
	}
	return code
}

// verifySeqEvidence 比对当前分析与保存的证据身份；差异写 stderr 并非零退出。
func verifySeqEvidence(root *action.CodeSeqNode, priorPath string) int {
	if root == nil || root.Meta == nil || root.Meta.Evidence == nil {
		fmt.Fprintln(os.Stderr, "error: 当前分析没有证据身份（--evidence 未生效）")
		return 1
	}
	prior, err := action.ReadSeqEvidenceFile(priorPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: 读取原证据失败: %v\n", err)
		return 1
	}
	diffs, err := action.CompareSeqEvidenceIdentity(root.Meta.Evidence, prior)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: 解析原证据身份失败: %v\n", err)
		return 1
	}
	if len(diffs) == 0 {
		fmt.Fprintln(os.Stderr, "证据身份匹配：同一次分析（源码 SHA/构建身份/分析器/配置/argv 一致）")
		return 0
	}
	fmt.Fprintln(os.Stderr, "证据身份不一致（这是新分析，不是原证据的复现）：")
	for _, d := range diffs {
		fmt.Fprintf(os.Stderr, "  · %s\n", d)
	}
	return 1
}

// renderCodeSeq 把时序树渲染成目标格式并输出（wrote=true 表示已写文件，
// 不再额外打印内容）。渲染失败返回非零退出码。
func renderCodeSeq(root *action.CodeSeqNode, mermaid, jsonOut bool, diagram, out string, evidence bool) (code int, wrote bool) {
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
		if diagram == "svg" {
			code, _ := renderCodeSeqSVG(m, out)
			return code, false
		}
		if diagram == "plantuml" {
			puml := mermaidSequenceToPlantuml(m)
			var renderErr error
			if puml != "" {
				var png []byte
				png, renderErr = plantumlRenderFunc(puml)
				if renderErr == nil {
					// todo20-10：渲染成功 ≠ 完整图——尺寸触顶必须显式提示
					// （PNG 任一维达到 PlantUML 上限时会被裁切）。
					if warn := plantumlSizeWarning(png); warn != "" {
						fmt.Fprintf(os.Stderr, "warning: %s\n", warn)
					}
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
			} else {
				renderErr = fmt.Errorf("PlantUML 转换结果为空")
			}
			// todo19-8：渲染失败必须明确；PNG 目标不得把文本写进 .png
			if out != "" && strings.EqualFold(filepath.Ext(out), ".png") {
				fallback, data := pumlSidecarPath(out), puml
				if data == "" {
					fallback = strings.TrimSuffix(out, filepath.Ext(out)) + ".mmd"
					data = m
				}
				if !writeOutFile(fallback, []byte(data)) {
					return 1, true
				}
				fmt.Fprintf(os.Stderr, "error: plantuml 渲染失败: %v；未生成 PNG，已写文本回退 %s\n", renderErr, fallback)
				fmt.Printf("已写入 %s（文本回退，不是 PNG）\n", fallback)
				return 1, true
			}
			if out != "" {
				if !writeOutFile(out, []byte(m)) {
					return 1, true
				}
				fmt.Fprintf(os.Stderr, "warning: plantuml 渲染失败: %v；已写 Mermaid 文本到 %s（不是图片）\n", renderErr, out)
				return 0, true
			}
			fmt.Fprintf(os.Stderr, "warning: plantuml 渲染失败: %v；输出 Mermaid 文本\n", renderErr)
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
	text := fmt.Sprintf("== %s 代码级时序 ==\n", root.Label) + seqText(root.Nodes, 1) + seqDiagText(root) + seqSceneText(root)
	if evidence {
		text += seqEvidenceText(root)
	}
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
	fmt.Fprintf(os.Stderr, "[seq.stage] sym=%d/%.3fms sql=%d/%.3fms sig=%d/%.3fms dispatch=%d/%.3fms cache=hit:%d miss:%d\n",
		timing.SymLookups, timing.SymMS, timing.SQLQueries, timing.SQLMS,
		timing.Signature, timing.SignatureMS, timing.Dispatch, timing.DispatchMS,
		timing.CacheHits, timing.CacheMisses)
}
