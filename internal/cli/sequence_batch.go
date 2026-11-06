package cli

// B11：sequence-batch——一次命令按 manifest 导出多流程 Markdown/时序图。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// seqBatchManifest 输入清单（只选择分析范围与事实文件）。
type seqBatchManifest struct {
	Version int            `json:"version"`
	Flows   []seqBatchFlow `json:"flows"`
	Formats []string       `json:"formats,omitempty"`
}

// seqBatchFlow 单个流程（B11b：可带运行时事实/组合选择/预算与场景）。
type seqBatchFlow struct {
	ID                  string   `json:"id"`
	Repo                string   `json:"repo"`
	Entry               string   `json:"entry"`
	RelatedEntries      []string `json:"relatedEntries,omitempty"`
	FrontendRoots       []string `json:"frontendRoots,omitempty"`
	RuntimeData         string   `json:"runtimeData,omitempty"`
	CombinationSelector string   `json:"combinationSelector,omitempty"`
	Depth               int      `json:"depth,omitempty"`
	ContinueSegments    int      `json:"continueSegments,omitempty"`
	Scene               []string `json:"scene,omitempty"`
	Timeout             string   `json:"timeout,omitempty"`
}

// seqBatchSummary JSON 摘要（稳定字段）。
type seqBatchSummary struct {
	Total    int      `json:"total"`
	Exported int      `json:"exported"`
	Failed   int      `json:"failed"`
	Files    []string `json:"files"`
}

// cmdSequenceBatch 实现 `codeintel sequence-batch --manifest <json> --out <dir> [--json]`。
func cmdSequenceBatch(args []string) int {
	manifestPath, outDir, jsonOut := "", "exported", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--manifest" && i+1 < len(args):
			manifestPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--manifest="):
			manifestPath = strings.TrimPrefix(a, "--manifest=")
		case a == "--out" && i+1 < len(args):
			outDir = args[i+1]
			i++
		case strings.HasPrefix(a, "--out="):
			outDir = strings.TrimPrefix(a, "--out=")
		case a == "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "error: 未知参数 %s\n", a)
				return 2
			}
		}
	}
	if manifestPath == "" {
		fmt.Fprintln(os.Stderr, "error: sequence-batch 需要 --manifest <flows.json>")
		return 2
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: 读取 manifest 失败: %v\n", err)
		return 1
	}
	var mf seqBatchManifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&mf); err != nil {
		fmt.Fprintf(os.Stderr, "error: manifest JSON 非法: %v\n", err)
		return 1
	}
	if len(mf.Flows) == 0 {
		fmt.Fprintln(os.Stderr, "error: manifest 没有 flows")
		return 1
	}
	formats := mf.Formats
	if len(formats) == 0 {
		formats = []string{"markdown"}
	}
	for _, f := range formats {
		if f != "markdown" && f != "svg" {
			fmt.Fprintf(os.Stderr, "error: 不支持的格式 %q（markdown|svg）\n", f)
			return 2
		}
	}
	// 先校验 id 与重复（不写任何文件）。
	seen := map[string]bool{}
	for _, fl := range mf.Flows {
		if fl.ID == "" || strings.ContainsAny(fl.ID, `/\`) || fl.ID == "." || fl.ID == ".." {
			fmt.Fprintf(os.Stderr, "error: flow id 非法 %q\n", fl.ID)
			return 2
		}
		if seen[fl.ID] {
			fmt.Fprintf(os.Stderr, "error: flow id 重复 %q\n", fl.ID)
			return 2
		}
		seen[fl.ID] = true
	}
	baseDir := filepath.Dir(manifestPath)
	// B11c：manifest 参数错误必须在打开索引/创建文件之前拒绝。
	inputs, verr := seqBatchValidateFlows(mf.Flows, baseDir)
	if verr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", verr)
		return 1
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: 创建输出目录失败: %v\n", err)
		return 1
	}
	summary := seqBatchSummary{Total: len(mf.Flows)}
	var index strings.Builder
	index.WriteString("# sequence-batch 导出\n\n| flow | entry | 状态 | 文件 |\n|---|---|---|---|\n")
	for fi, fl := range mf.Flows {
		repo := fl.Repo
		if !filepath.IsAbs(repo) {
			repo = filepath.Join(baseDir, repo)
		}
		repo, _ = filepath.Abs(repo)
		acts, closeFn, oerr := openRepoActions(repo)
		if oerr != nil {
			summary.Failed++
			fmt.Fprintf(os.Stderr, "error: flow %s 打开仓库失败: %v\n", fl.ID, oerr)
			index.WriteString(fmt.Sprintf("| %s | %s | failed（%s） | - |\n", fl.ID, fl.Entry, escapeMD(oerr.Error())))
			continue
		}
		depth := fl.Depth
		if depth <= 0 {
			depth = 12
		}
		ctx := context.Background()
		var cancel context.CancelFunc
		if inputs[fi].timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, inputs[fi].timeout)
		}
		runtimePath := inputs[fi].runtimePath
		req := action.CodeSequenceRequest{Target: fl.Entry, RepoAbs: repo, Depth: depth,
			ContinueSegments: fl.ContinueSegments, Scene: action.NewScene(fl.Scene),
			RuntimeDataPath: runtimePath, CombinationSelector: fl.CombinationSelector,
			RelatedEntries: fl.RelatedEntries, FrontendRoots: fl.FrontendRoots,
			Ctx: ctx}
		if fl.FrontendRoots != nil {
			for i := range fl.FrontendRoots {
				if !filepath.IsAbs(fl.FrontendRoots[i]) {
					fl.FrontendRoots[i] = filepath.Join(repo, fl.FrontendRoots[i])
				}
			}
			req.FrontendRoots = fl.FrontendRoots
		}
		root, cerr := acts.CodeSequence(req)
		if cancel != nil {
			cancel()
		}
		closeFn()
		if cerr != nil || root == nil {
			summary.Failed++
			reason := "analysis failed"
			if cerr != nil {
				reason = cerr.Error()
			}
			fmt.Fprintf(os.Stderr, "error: flow %s 分析失败: %s\n", fl.ID, reason)
			index.WriteString(fmt.Sprintf("| %s | %s | failed（%s） | - |\n", fl.ID, fl.Entry, escapeMD(reason)))
			continue
		}
		var files []string
		failed := false
		writeOne := func(baseName, title string, tree *action.CodeSeqNode) {
			// B10d：Markdown 使用完整流程事实（含关联入口/实例）并写入可渲染
			// PlantUML 时序图；图与 SVG 复用同一渲染树。
			m := renderCodeSeqMermaid(tree)
			puml := mermaidSequenceToPlantuml(m)
			if puml == "" {
				failed = true
				fmt.Fprintf(os.Stderr, "error: flow %s PlantUML 转换为空（不写 Markdown）\n", fl.ID)
				return
			}
			mdPath := filepath.Join(outDir, baseName+".md")
			md := fmt.Sprintf("# %s\n\n`%s`\n\n%s\n```text\n%s```\n\n```plantuml\n%s```\n",
				title, fl.Entry, seqBatchReplayLine(fl, runtimePath, depth),
				seqTextRoot(tree), puml)
			if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
				failed = true
				fmt.Fprintf(os.Stderr, "error: flow %s 写 Markdown 失败: %v\n", fl.ID, err)
			} else {
				files = append(files, baseName+".md")
			}
			if failed || !containsStr(formats, "svg") {
				return
			}
			svg, rerr := plantumlRenderSVGFunc(puml)
			if rerr != nil || len(svg) == 0 {
				failed = true
				if rerr == nil {
					rerr = fmt.Errorf("SVG 渲染结果为空")
				}
				fmt.Fprintf(os.Stderr, "error: flow %s SVG 渲染失败: %v\n", fl.ID, rerr)
				return
			}
			if werr := os.WriteFile(filepath.Join(outDir, baseName+".svg"), svg, 0o644); werr != nil {
				failed = true
				fmt.Fprintf(os.Stderr, "error: flow %s 写 SVG 失败: %v\n", fl.ID, werr)
				return
			}
			files = append(files, baseName+".svg")
		}
		// B10b/B11b：有组合场景时逐场景导出（不把同一张图复制多份）。
		if root.Workflow != nil && len(root.Workflow.Scenarios) > 0 {
			for i, sc := range root.Workflow.Scenarios {
				if sc.Sequence == nil {
					continue
				}
				name := fl.ID + "-scenario-" + itoaSeqInt(i+1)
				writeOne(name, fl.ID+"（"+seqBatchParams(sc.Parameters)+"）", sc.Sequence)
			}
		} else {
			writeOne(fl.ID, fl.ID, root)
		}
		status := "exported"
		if root.Workflow != nil && root.Workflow.Partial {
			status = "exported（partial）"
		}
		if failed {
			summary.Failed++
			status = "failed"
		} else {
			summary.Exported++
			summary.Files = append(summary.Files, files...)
		}
		index.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", fl.ID, fl.Entry, status, strings.Join(files, ", ")))
	}
	indexPath := filepath.Join(outDir, "index.md")
	if err := os.WriteFile(indexPath, []byte(index.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: 写 index.md 失败: %v\n", err)
		return 1
	}
	summary.Files = append(summary.Files, "index.md")
	if jsonOut {
		encodeJSON(summary)
	} else {
		fmt.Printf("导出 %d/%d（失败 %d）→ %s\n", summary.Exported, summary.Total, summary.Failed, outDir)
	}
	if summary.Failed > 0 {
		return 1
	}
	return 0
}

func escapeMD(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}
