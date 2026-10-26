//go:build integration

package integration

// todo13-14 A2：原始 argv 从非模块目录重放（空格/括号/#1/输出选项），
// 同 SHA 重建与配置变化时明确报告身份差异。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTodo13EvidenceIdentityRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(wd)
	bin := filepath.Join(t.TempDir(), "codeintel")
	build := exec.Command("go", "build", "-o", bin, "./cmd/codeintel")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build codeintel: %v\n%s", err, out)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	reg := filepath.Join(root, "registry")
	fixture := filepath.Join(root, "repo (v2) space")
	nonModule := filepath.Join(root, "nonmodule")
	outDir := filepath.Join(root, "out dir")
	for _, d := range []string{home, reg, fixture, nonModule, outDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "dummy"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// 全局配置（stop_packages）——HOME 隔离，验证配置身份
	cfgDir := filepath.Join(home, ".codeintel")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("seq:\n  stop_packages: [example.com/aaa]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile := func(rel, body string) {
		t.Helper()
		p := filepath.Join(fixture, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("go.mod", "module example.com/sp\n\ngo 1.21\n")
	writeFile("main.go", `package sp

type Handler struct{ State string }

func Route(region string) string { return region }

func (h *Handler) Run(region string) string {
	h.State = Route(region)
	return h.State
}
`)
	env := append(os.Environ(), "HOME="+home, "CODEINTEL_REGISTRY_DIR="+reg)
	run := func(dir string, args ...string) (int, string, string) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = env
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		return code, stdout.String(), stderr.String()
	}
	if gitAvailable() {
		runGit(t, fixture, "init", "-b", "main", ".")
		runGit(t, fixture, "config", "user.email", "t@t")
		runGit(t, fixture, "config", "user.name", "t")
		runGit(t, fixture, "add", ".")
		runGit(t, fixture, "commit", "-m", "init")
	}
	if code, _, stderr := run(nonModule, "init", "--repo", fixture, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d\n%s", code, stderr)
	}

	sym := "symbol:go:example.com/sp:(Handler).Run"
	outFile := filepath.Join(outDir, "evidence output.json")
	// 原始 argv（非模块目录 + 空格/括号/#1/输出选项）——用 shell 执行验证 quoting
	raw := bin + " query sequence '" + sym + "' --repo '" + fixture +
		"' --code --depth 2 --scene 'region=#1' --focus 'return' --evidence" +
		" --format mermaid --out '" + outFile + "' --json"
	shellRun := func(shell string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(shell, "-c", "cd '"+nonModule+"' && "+raw)
		cmd.Env = env
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return code, stdout.String(), stderr.String()
	}
	code, stdout, stderr := shellRun("sh")
	if code != 0 {
		t.Fatalf("raw argv replay exit = %d\n%s\n%s", code, stdout, stderr)
	}
	outBytes, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("应写输出文件: %v（stdout=%s）", err, stdout)
	}
	out := string(outBytes)
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("replay JSON: %v\n%s", err, out)
	}
	meta, _ := res["meta"].(map[string]any)
	id, _ := meta["evidence"].(map[string]any)
	if id == nil {
		t.Fatalf("meta.evidence 缺失: %+v", meta)
	}
	argv, _ := id["argv"].([]any)
	argvStr := strings.Join(interfaceStrings(argv), " ")
	for _, want := range []string{"'region=#1'", "'return'", "'" + outFile + "'",
		"'symbol:go:example.com/sp:(Handler).Run'", "'" + fixture + "'"} {
		if !strings.Contains(argvStr, want) {
			t.Errorf("argv 应逐项保留 %q:\n%s", want, argvStr)
		}
	}
	if id["format"] != "mermaid" || id["out"] != outFile {
		t.Errorf("输出选项应进身份/请求: %+v", id)
	}
	if id["build_id"] == "" || id["index_sha"] == "" {
		t.Errorf("应含索引构建身份: %+v", id)
	}
	// zsh 可用时同样可执行
	if _, err := exec.LookPath("zsh"); err == nil {
		code, _, zerr := shellRun("zsh")
		if code != 0 {
			t.Errorf("zsh 重放失败: code=%d\n%s", code, zerr)
		}
	}
	// 保存证据（整份输出）
	evidenceFile := filepath.Join(root, "prior.json")
	if err := os.WriteFile(evidenceFile, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}

	// 身份匹配对照（未重建）
	code, _, stderr = run(nonModule, "query", "sequence", sym, "--repo", fixture,
		"--code", "--depth", "2", "--scene", "region=#1", "--focus", "return",
		"--evidence", "--format", "mermaid", "--out", outFile, "--json",
		"--verify-evidence", evidenceFile)
	if code != 0 || !strings.Contains(stderr, "证据身份匹配") {
		t.Fatalf("同身份应匹配: code=%d\n%s", code, stderr)
	}

	// 同 SHA 重建（git：同 commit 再 init）→ 构建身份差异
	if gitAvailable() {
		if code, _, stderr := run(nonModule, "init", "--repo", fixture, "--progress", "none"); code != 0 {
			t.Fatalf("re-init exit = %d\n%s", code, stderr)
		}
		// 重新生成本次证据（新构建身份）
		code, _, stderr := run(nonModule, "query", "sequence", sym, "--repo", fixture,
			"--code", "--depth", "2", "--scene", "region=#1", "--focus", "return",
			"--evidence", "--format", "mermaid", "--out", outFile, "--json")
		if code != 0 {
			t.Fatalf("post-rebuild query exit = %d\n%s", code, stderr)
		}
		out2, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatal(err)
		}
		prior2 := filepath.Join(root, "prior2.json")
		if err := os.WriteFile(prior2, out2, 0o644); err != nil {
			t.Fatal(err)
		}
		if code, _, stderr := run(nonModule, "query", "sequence", sym, "--repo", fixture,
			"--code", "--depth", "2", "--scene", "region=#1", "--focus", "return",
			"--evidence", "--format", "mermaid", "--out", outFile, "--json",
			"--verify-evidence", prior2); code != 0 || !strings.Contains(stderr, "证据身份匹配") {
			t.Errorf("重建后同身份应匹配: code=%d\n%s", code, stderr)
		}
		// 旧证据 vs 新构建 → 明确报告构建身份变化、源码 SHA 不变
		code, _, stderr = run(nonModule, "query", "sequence", sym, "--repo", fixture,
			"--code", "--depth", "2", "--scene", "region=#1", "--focus", "return",
			"--evidence", "--format", "mermaid", "--out", outFile, "--json",
			"--verify-evidence", evidenceFile)
		if code == 0 || !strings.Contains(stderr, "索引构建身份变化") {
			t.Errorf("同 SHA 重建应报告构建身份变化: code=%d\n%s", code, stderr)
		}
		if strings.Contains(stderr, "源码 SHA 变化") {
			t.Errorf("源码 SHA 未变不应报告变化:\n%s", stderr)
		}
	}

	// 配置变化（stop_packages）→ 配置身份差异
	code, _, stderr = run(nonModule, "query", "sequence", sym, "--repo", fixture,
		"--code", "--depth", "2", "--scene", "region=#1", "--focus", "return",
		"--evidence", "--format", "mermaid", "--out", outFile, "--json")
	if code != 0 {
		t.Fatalf("config query exit = %d\n%s", code, stderr)
	}
	out3, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	prior3 := filepath.Join(root, "prior3.json")
	if err := os.WriteFile(prior3, out3, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("seq:\n  stop_packages: [example.com/bbb]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = run(nonModule, "query", "sequence", sym, "--repo", fixture,
		"--code", "--depth", "2", "--scene", "region=#1", "--focus", "return",
		"--evidence", "--format", "mermaid", "--out", outFile, "--json",
		"--verify-evidence", prior3)
	if code == 0 || !strings.Contains(stderr, "配置身份变化") ||
		!strings.Contains(stderr, "example.com/bbb") {
		t.Errorf("配置变化应报告差异: code=%d\n%s", code, stderr)
	}
}

func interfaceStrings(xs []any) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
