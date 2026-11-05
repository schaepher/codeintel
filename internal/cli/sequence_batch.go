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

// seqBatchFlow 单个流程。
type seqBatchFlow struct {
	ID             string   `json:"id"`
	Repo           string   `json:"repo"`
	Entry          string   `json:"entry"`
	RelatedEntries []string `json:"relatedEntries,omitempty"`
	FrontendRoots  []string `json:"frontendRoots,omitempty"`
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
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: 创建输出目录失败: %v\n", err)
		return 1
	}
	baseDir := filepath.Dir(manifestPath)
	summary := seqBatchSummary{Total: len(mf.Flows)}
	var index strings.Builder
	index.WriteString("# sequence-batch 导出\n\n| flow | entry | 状态 | 文件 |\n|---|---|---|---|\n")
	for _, fl := range mf.Flows {
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
		req := action.CodeSequenceRequest{Target: fl.Entry, RepoAbs: repo, Depth: 12,
			RelatedEntries: fl.RelatedEntries, FrontendRoots: fl.FrontendRoots,
			Ctx: context.Background()}
		if fl.FrontendRoots != nil {
			for i := range fl.FrontendRoots {
				if !filepath.IsAbs(fl.FrontendRoots[i]) {
					fl.FrontendRoots[i] = filepath.Join(repo, fl.FrontendRoots[i])
				}
			}
			req.FrontendRoots = fl.FrontendRoots
		}
		root, cerr := acts.CodeSequence(req)
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
		mdPath := filepath.Join(outDir, fl.ID+".md")
		md := fmt.Sprintf("# %s\n\n`%s`\n\n```text\n%s```\n", fl.ID, fl.Entry, seqTextRoot(root))
		if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "error: flow %s 写 Markdown 失败: %v\n", fl.ID, err)
		} else {
			files = append(files, fl.ID+".md")
		}
		if !failed && containsStr(formats, "svg") {
			m := renderCodeSeqMermaid(root)
			puml := mermaidSequenceToPlantuml(m)
			var svg []byte
			var rerr error
			if puml != "" {
				svg, rerr = plantumlRenderSVGFunc(puml)
			}
			if puml == "" || rerr != nil || len(svg) == 0 {
				failed = true
				if rerr == nil {
					rerr = fmt.Errorf("SVG 渲染结果为空")
				}
				fmt.Fprintf(os.Stderr, "error: flow %s SVG 渲染失败: %v\n", fl.ID, rerr)
			} else if werr := os.WriteFile(filepath.Join(outDir, fl.ID+".svg"), svg, 0o644); werr != nil {
				failed = true
				fmt.Fprintf(os.Stderr, "error: flow %s 写 SVG 失败: %v\n", fl.ID, werr)
			} else {
				files = append(files, fl.ID+".svg")
			}
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
