package action

// yamlEditor 测试（批次 C 随迁自 cli/wiki_ai_test.go）：围栏剥离 +
// 合并保留原注释 + AI 初稿标注 + 追加缺失键。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestStripYAMLFence：AI 输出 yaml 围栏剥离（```yaml / ``` 变体）。
func TestStripYAMLFence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```yaml\ndescription: x\n```", "description: x"},
		{"```\ndescription: y\n```", "description: y"},
		{"description: z", "description: z"},
		{"```yaml\ndescription: a\n", "description: a"}, // 缺尾围栏也容忍
	}
	for _, c := range cases {
		if got := StripYAMLFence(c.in); got != c.want {
			t.Errorf("StripYAMLFence(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestYAMLEditorMerge：合并保留原注释 + AI 初稿标注 + 追加缺失键。
func TestYAMLEditorMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wiki.yaml")
	orig := "# 人工注释\nproject:\n  description: 项目\n\ntables:\n  - name: order_tab\n    alias: 订单表\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadYAMLEditor(path)
	if err != nil {
		t.Fatal(err)
	}
	e.SetModuleDesc("example.com/app/internal/agent", "LLM 代理层")
	e.SetTableAlias("user_tab", "用户表")
	e.SetColumnComments("order_tab", map[string]string{"order_no": "订单号"})
	if err := e.Save(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{"# 人工注释", "# AI 初稿", "description: LLM 代理层", "alias: 用户表", "comment: 订单号"} {
		if !strings.Contains(s, want) {
			t.Errorf("合并结果缺 %q:\n%s", want, s)
		}
	}
	// 重新解析验证结构合法
	var out struct {
		Modules []struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		} `yaml:"modules"`
		Tables []struct {
			Name  string `yaml:"name"`
			Alias string `yaml:"alias"`
		} `yaml:"tables"`
	}
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("合并后 yaml 解析失败: %v\n%s", err, s)
	}
	if len(out.Modules) != 1 || out.Modules[0].Description != "LLM 代理层" {
		t.Errorf("modules = %+v", out.Modules)
	}
	if len(out.Tables) != 2 {
		t.Errorf("tables = %+v", out.Tables)
	}
}
