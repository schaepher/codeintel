package cli

// todo13-14：证据身份在 CLI 中可观察；--verify-evidence 区分"新分析"与
// "复现原证据"。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestSeqEvidenceIdentityCLI(t *testing.T) {
	dir := seedCodeSeqRepo(t)
	db0, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewRepo(db0).Save(&domain.BuildMeta{BuildID: "built-1",
		ToolName: "test", Status: domain.BuildSuccess, CommitSHA: "sha-same"}); err != nil {
		t.Fatal(err)
	}
	db0.Close()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("seq:\n  stop_packages: [example.com/a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := agentConfigPath
	agentConfigPath = func() string { return cfg }
	t.Cleanup(func() { agentConfigPath = old })

	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "Prepare", "--repo", dir, "--code", "--json",
			"--evidence", "--depth", "1", "--focus", "return", "--scene", "cart=#1",
			"--format", "mermaid"}); code != 0 {
			t.Errorf("sequence exit = %d", code)
		}
	})
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	meta, _ := root["meta"].(map[string]any)
	id, _ := meta["evidence"].(map[string]any)
	if id == nil {
		t.Fatalf("meta.evidence 缺失: %+v", meta)
	}
	for _, k := range []string{"argv", "cli_version", "analyzer_version", "build_id",
		"config_hash", "repo", "symbol", "depth", "focus", "scene", "format"} {
		if _, ok := id[k]; !ok {
			t.Errorf("身份应含 %q: %+v", k, id)
		}
	}
	argv := strings.Join(anyStrings(id["argv"]), "\x00")
	for _, want := range []string{"cart=#1", "return", "mermaid", "--evidence"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv 应含 %q: %s", want, argv)
		}
	}
	prior := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(prior, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}

	// 同身份：验证通过
	code := 0
	stderr := captureStderr(func() {
		code = cmdQuery([]string{"sequence", "Prepare", "--repo", dir, "--code", "--json",
			"--evidence", "--depth", "1", "--focus", "return", "--scene", "cart=#1",
			"--format", "mermaid", "--verify-evidence", prior})
	})
	if code != 0 || !strings.Contains(stderr, "证据身份匹配") {
		t.Errorf("同身份应通过: code=%d\n%s", code, stderr)
	}

	// 同 SHA 重建（新 build id）→ 构建身份变化
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewRepo(db).Save(&domain.BuildMeta{BuildID: "rebuilt-2",
		ToolName: "test", Status: domain.BuildSuccess, CommitSHA: "sha-same"}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	stderr = captureStderr(func() {
		code = cmdQuery([]string{"sequence", "Prepare", "--repo", dir, "--code", "--json",
			"--evidence", "--depth", "1", "--focus", "return", "--scene", "cart=#1",
			"--format", "mermaid", "--verify-evidence", prior})
	})
	if code == 0 || !strings.Contains(stderr, "索引构建身份变化") ||
		strings.Contains(stderr, "源码 SHA 变化") {
		t.Errorf("同 SHA 重建应报告构建身份变化: code=%d\n%s", code, stderr)
	}

	// 配置变化（停止包）→ 报告配置差异
	prior2Out := captureStdout(func() {
		_ = cmdQuery([]string{"sequence", "Prepare", "--repo", dir, "--code", "--json",
			"--evidence", "--depth", "1", "--focus", "return", "--scene", "cart=#1",
			"--format", "mermaid"})
	})
	prior2 := filepath.Join(t.TempDir(), "evidence2.json")
	if err := os.WriteFile(prior2, []byte(prior2Out), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("seq:\n  stop_packages: [example.com/b]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr = captureStderr(func() {
		code = cmdQuery([]string{"sequence", "Prepare", "--repo", dir, "--code", "--json",
			"--evidence", "--depth", "1", "--focus", "return", "--scene", "cart=#1",
			"--format", "mermaid", "--verify-evidence", prior2})
	})
	if code == 0 || !strings.Contains(stderr, "配置身份变化") ||
		!strings.Contains(stderr, "example.com/b") {
		t.Errorf("配置变化应报告差异: code=%d\n%s", code, stderr)
	}
}

func anyStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
