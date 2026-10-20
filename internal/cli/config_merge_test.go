package cli

// S7 测试：config merge——旧配置缺配置项时从模板补默认（保留用户
// 值）；无缺失时提示。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// TestConfigMergeNestedScalarPlacement：嵌套标量缺失项（seq.depth）应插在
// **父块末尾**，不落在下一个顶层键的前导注释之下——否则 `# S4：plantuml.jar`
// 的注释会看起来是 seq.depth 的说明（注释错位）。
func TestConfigMergeNestedScalarPlacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// 旧配置：缺 seq.depth，但保留了模板里 plantuml_jar 的前导注释
	old := `seq:
  stop_packages: []
  filter_pkgs: []

# S4：plantuml.jar 路径（配置了用它转图片）
plantuml_jar: ""
`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := agentConfigPath
	agentConfigPath = func() string { return path }
	defer func() { agentConfigPath = orig }()

	if code := cmdConfigMerge(); code != 0 {
		t.Fatalf("merge 退出码 = %d; want 0", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	t.Logf("merge 后内容:\n%s", s)
	di, ci := strings.Index(s, "depth: 3"), strings.Index(s, "# S4：plantuml.jar")
	if di < 0 {
		t.Fatalf("应补 seq.depth:\n%s", s)
	}
	if ci >= 0 && di > ci {
		t.Errorf("seq.depth 应插在父块（seq）内，不能落在下一个键的注释之下:\n%s", s)
	}
	// 解析为 YAML 后 seq.depth 必须是 3（插入位置不能破坏层级）
	var cfg struct {
		Seq struct {
			Depth int `yaml:"depth"`
		} `yaml:"seq"`
	}
	var doc map[string]any
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("merge 后配置不是合法 YAML: %v\n%s", err, s)
	}
	if err := yaml.Unmarshal(got, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Seq.Depth != 3 {
		t.Errorf("seq.depth = %d; want 3（插入位置错→层级错）:\n%s", cfg.Seq.Depth, s)
	}
}

// TestConfigMergeNoDuplicateComments：同一父块下多个同辈键缺失时，模板里
// 共用的前导注释**不得重复插入**（原实现每个缺失键都带回它前面的注释块
// ——`make install` 每次升级都会把注释堆一遍）。
func TestConfigMergeNoDuplicateComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// 旧配置：模板去掉 seq.depth 后的样子（其余注释都在）
	lines := strings.Split(configExample, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) == "depth: 3" {
			continue
		}
		kept = append(kept, l)
	}
	old := strings.Join(kept, "\n")
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := agentConfigPath
	agentConfigPath = func() string { return path }
	defer func() { agentConfigPath = orig }()

	if code := cmdConfigMerge(); code != 0 {
		t.Fatalf("merge 退出码 = %d; want 0", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	t.Logf("merge 后内容:\n%s", s)
	if n := strings.Count(s, "# 工具函数识别（query helpers"); n != 1 {
		t.Errorf("共用前导注释重复 %d 次（应为 1）:\n%s", n, s)
	}
	if n := strings.Count(s, "  depth: 3"); n != 1 {
		t.Errorf("seq.depth 出现 %d 次（应为 1）:\n%s", n, s)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("merge 后不是合法 YAML: %v\n%s", err, s)
	}
	// 幂等：再次 merge 无缺失
	if code := cmdConfigMerge(); code != 0 {
		t.Fatalf("二次 merge 退出码 = %d; want 0", code)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != s {
		t.Errorf("二次 merge 不应再改文件:\n--- 一次 ---\n%s\n--- 二次 ---\n%s", s, string(again))
	}
}
