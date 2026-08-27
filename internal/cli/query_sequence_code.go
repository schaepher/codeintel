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
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
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
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if root == nil {
		fmt.Fprintf(os.Stderr, "error: 代码级时序不可用（符号解析失败或源码缺失——可尝试不带 --code 用索引调用链）\n")
		return 1
	}
	if jsonOut {
		if out != "" {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetIndent("", "  ")
			_ = enc.Encode(root)
			if !writeOutFile(out, buf.Bytes()) {
				return 1
			}
			return 0
		}
		encodeJSON(root)
		return 0
	}
	if mermaid {
		m := renderCodeSeqMermaid(root)
		if diagram == "plantuml" {
			if puml := mermaidSequenceToPlantuml(m); puml != "" {
				if png, err := plantumlRenderFunc(puml); err == nil {
					if out != "" {
						if !writeOutFile(out, png) {
							return 1
						}
						return 0
					}
					fmt.Println("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
					return 0
				}
			}
			// 转换/渲染失败 → 写文本（mermaid 原文）
		}
		if out != "" {
			if !writeOutFile(out, []byte(m)) {
				return 1
			}
			return 0
		}
		fmt.Print(m)
		return 0
	}
	// 默认文本：缩进树（源码顺序 + 分支/循环标注）
	text := fmt.Sprintf("== %s 代码级时序 ==\n", root.Label) + seqText(root.Nodes, 1)
	if out != "" {
		if !writeOutFile(out, []byte(text)) {
			return 1
		}
		return 0
	}
	fmt.Print(text)
	return 0
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
