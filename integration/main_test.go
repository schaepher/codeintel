//go:build integration

package integration

// todo13-13：集成测试使用独立全局注册表目录（不污染真实 ~/.codeintel，
// 也避免 ext-chain 在数千个历史临时仓库里逐个打开）。
import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "codeintel-registry-")
	if err == nil {
		os.Setenv("CODEINTEL_REGISTRY_DIR", dir)
	}
	code := m.Run()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}
