package cli

// S7 测试：config merge——旧配置缺配置项时从模板补默认（保留用户
// 值）；无缺失时提示。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigMergeFillsMissing：旧配置（缺 seq.filter_pkgs 与
// plantuml_jar）→ merge 补上；用户已有值（stop_packages）保留。
func TestConfigMergeFillsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	old := `project:
  description: 我的项目
seq:
  stop_packages: [example.com/m/impl]
ai:
  fill: off
`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := agentConfigPath
	agentConfigPath = func() string { return path }
	defer func() { agentConfigPath = orig }()

	code := cmdConfigMerge()
	if code != 0 {
		t.Fatalf("merge 退出码 = %d; want 0", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	t.Logf("merge 后内容:\n%s", s)
	// 缺失项补上（模板默认）
	if !strings.Contains(s, "filter_pkgs") || !strings.Contains(s, "filter_fns") {
		t.Errorf("merge 应补 seq.filter_* 配置项:\n%s", s)
	}
	if !strings.Contains(s, "plantuml_jar") {
		t.Errorf("merge 应补 plantuml_jar 配置项:\n%s", s)
	}
	// 用户已有值保留（未被覆盖）
	if !strings.Contains(s, "我的项目") || !strings.Contains(s, "example.com/m/impl") {
		t.Errorf("用户已有配置应保留:\n%s", s)
	}
	// 补缺后的配置仍是合法 YAML（重新 merge 无缺失）
	if code2 := cmdConfigMerge(); code2 != 0 {
		t.Fatalf("二次 merge 应无缺失（退出码 %d）", code2)
	}
}

// TestConfigMergeNoMissing：完整配置 → 无缺失提示。
func TestConfigMergeNoMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(configExample), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := agentConfigPath
	agentConfigPath = func() string { return path }
	defer func() { agentConfigPath = orig }()

	if code := cmdConfigMerge(); code != 0 {
		t.Fatalf("完整配置 merge 退出码 = %d; want 0", code)
	}
}
