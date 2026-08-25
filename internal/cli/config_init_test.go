package cli

// R58 全局配置自动初始化测试：~/.codeintel/config.yaml 不存在时首次
// 读配置自动从内置模板创建（含全部选项 + 默认值 + 注释）；已存在不
// 覆盖（用户改过的不丢）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureGlobalConfig：不存在 → 自动创建（模板内容含注释与默认值）；
// 已存在 → 不覆盖（用户改动保留）。
func TestEnsureGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	restore := injectAgentConfigPath(t, cfgPath)
	defer restore()

	// 文件不存在 → 自动初始化
	ensureGlobalConfig()
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("初始化后应存在 config.yaml: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"# codeintel 全局配置", // 注释
		"agent: auto",          // 默认值
		"ai:",                  // 选项段
		"domains: auto",
		"fill: auto",
		"ask: auto",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("初始化文件应含 %q:\n%s", want, s)
		}
	}
	// 幂等 + 不覆盖：改文件后再初始化
	modified := s + "\n# 用户自定义\nagent: claude\n"
	if err := os.WriteFile(cfgPath, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureGlobalConfig()
	b2, _ := os.ReadFile(cfgPath)
	if string(b2) != modified {
		t.Error("已存在的配置不应被覆盖（用户改动保留）")
	}
}

// TestConfigExampleSync：仓库根 config.yaml.example 与内置模板一致
// （防止两份漂移——修改模板需同步）。
func TestConfigExampleSync(t *testing.T) {
	root := repoRootForTest(t)
	ex, err := os.ReadFile(filepath.Join(root, "config.yaml.example"))
	if err != nil {
		t.Fatalf("仓库根 config.yaml.example 缺失: %v", err)
	}
	if strings.TrimSpace(string(ex)) != strings.TrimSpace(configExample) {
		t.Error("config.yaml.example 与内置模板不一致——请同步（cp internal/cli/config_example.yaml config.yaml.example）")
	}
}

// repoRootForTest 定位仓库根（向上找 go.mod——测试 cwd 为包目录）。
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("未找到仓库根（go.mod）")
		}
		dir = parent
	}
}

// TestResolveAgentInitializesConfig：resolveAgent 路径也会触发初始化
// （agentFromConfig 调用 ensureGlobalConfig）。
func TestResolveAgentInitializesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "sub", "config.yaml")
	restore := injectAgentConfigPath(t, cfgPath)
	defer restore()
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatal("预置文件不应存在")
	}
	// 不传 agent → resolveAgent 内部走 agentFromConfig（触发初始化）→
	// auto 检测（测试环境可能无 CLI——失败也验证了文件已创建）
	_, _ = resolveAgent("")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("resolveAgent 应触发配置初始化: %v", err)
	}
}
