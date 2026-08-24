package cli

import (
	"errors"
	"os"
	"testing"
	"time"
)

// Q238：cli 测试统一把全局注册表目录指向临时目录——cmdInit/cmdUpdate/
// cmdClean 的注册钩子不会污染真实 ~/.codeintel。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "codeintel-registry-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	old := registryDirFn
	registryDirFn = func() string { return dir }
	// R34：测试环境禁止真实 AI 调用（双保险——runner 注入拒绝 +
	// CODEINTEL_SKIP_DOMAINS；即使 env 遗漏也快速失败而非真调 claude）
	os.Setenv("CODEINTEL_SKIP_DOMAINS", "1")
	oldRunner := agentRunner
	agentRunner = func(agent, prompt string, timeout time.Duration) (string, error) {
		return "", errors.New("测试环境禁止真实 AI 调用（injectRunner 覆盖后可用）")
	}
	code := m.Run()
	agentRunner = oldRunner
	registryDirFn = old
	os.Exit(code)
}
