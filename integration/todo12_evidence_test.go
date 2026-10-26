//go:build integration

package integration

// todo12-14：证据命令可以安全重放相同分析。
//   - evidence.query 含规范化仓库/canonical ID/深度/focus/scene，且 shell
//     quoting 正确（仓库路径含空格、方法名含括号）
//   - 从非模块目录用真实 bash/zsh 执行该命令，结果与进程内分析一致
//     （忽略计时等非语义字段）

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const todo12EvidenceSrc = `package regression

type Handler struct{ State string }

func Normalize(s string) string { return s }

func (h *Handler) Wrap(region string) (string, error) {
	status := "not_started"
	if region == "CN" {
		h.State = status
		status = Normalize("accepted")
	}
	return status, nil
}
`

func TestTodo12EvidenceReplay(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo with space")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"),
		[]byte("module example.com/codeintel-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "flow.go"), []byte(todo12EvidenceSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, repoDir)
	if code := runCLI(t, "init", "--repo", repoDir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}

	sym := "symbol:go:example.com/codeintel-regression:(Handler).Wrap"
	args := []string{"query", "sequence", sym, "--repo", repoDir, "--code", "--depth", "2",
		"--focus", "status,return", "--scene", "region=CN", "--scene", "platform=ios",
		"--json", "--evidence"}
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("sequence exit = %d; %s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	// 找到带 evidence.query 的节点
	var query string
	var snap map[string]any
	walkSeqJSON(root, func(n map[string]any) {
		ev, _ := n["evidence"].(map[string]any)
		if ev == nil || query != "" {
			return
		}
		if q, _ := ev["query"].(string); q != "" {
			query = q
			snap, _ = ev["request"].(map[string]any)
		}
	})
	if query == "" {
		t.Fatalf("证据应含可重放查询命令: %+v", root)
	}
	if snap == nil {
		t.Fatalf("证据应含规范化请求快照")
	}
	if snap["repo"] != repoDir || snap["symbol"] != sym || snap["depth"] != float64(2) {
		t.Errorf("请求快照字段不完整: %+v", snap)
	}
	if !strings.Contains(query, "'"+repoDir+"'") {
		t.Errorf("仓库路径应 shell-quoted: %s", query)
	}
	if !strings.Contains(query, "(Handler).Wrap") {
		t.Errorf("命令应使用 canonical ID: %s", query)
	}
	for _, want := range []string{"--depth 2", "--focus 'status'", "--focus 'return'", "--scene 'platform=ios'", "--scene 'region=CN'"} {
		if !strings.Contains(query, want) {
			t.Errorf("命令应含 %q: %s", want, query)
		}
	}

	// 构建真实 CLI 二进制（PATH 让命令可直接执行）
	binDir := filepath.Join(base, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "codeintel")
	build := exec.Command("go", "build", "-o", bin, "./cmd/codeintel")
	build.Dir = moduleRoot(t)
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build codeintel: %v\n%s", err, b)
	}
	env := append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cwd := t.TempDir() // 非模块目录

	shells := [][]string{{"bash", "-c"}}
	if _, err := exec.LookPath("zsh"); err == nil {
		shells = append(shells, []string{"zsh", "-fc"})
	}
	for _, sh := range shells {
		cmd := exec.Command(sh[0], append(sh[1:], query)...)
		cmd.Dir = cwd
		cmd.Env = env
		replayed, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s 执行证据命令失败: %v\n命令: %s", sh[0], err, query)
		}
		var got map[string]any
		if err := json.Unmarshal(replayed, &got); err != nil {
			t.Fatalf("%s 输出非 JSON: %v\n%s", sh[0], err, replayed)
		}
		if diff := normalizedSeqDiff(root, got); diff != "" {
			t.Errorf("%s 重放结果与进程内分析不一致: %s\n命令: %s", sh[0], diff, query)
		}
	}
}

// normalizedSeqDiff 比较两次 sequence JSON（去掉计时/trace 等非语义字段）。
func normalizedSeqDiff(a, b map[string]any) string {
	na := normalizeSeqJSON(a)
	nb := normalizeSeqJSON(b)
	ja, _ := json.Marshal(na)
	jb, _ := json.Marshal(nb)
	if string(ja) == string(jb) {
		return ""
	}
	return "a=" + truncate(string(ja), 600) + " b=" + truncate(string(jb), 600)
}

func normalizeSeqJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			lk := strings.ToLower(k)
			// todo13-14：证据身份（版本/构建 argv）按环境不同，不属于语义树
			if lk == "timing" || lk == "trace_id" || lk == "span_id" || lk == "elapsed" || lk == "evidence" {
				continue
			}
			out[k] = normalizeSeqJSON(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, val := range x {
			out = append(out, normalizeSeqJSON(val))
		}
		return out
	default:
		return v
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(wd) // integration/ → 仓库根
}

// 确保排序相关工具被引用（规范化本身的稳定性）。
var _ = sort.Strings
