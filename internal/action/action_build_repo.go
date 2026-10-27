package action

// P1-03（构建流程收口）：构建目标的发现与校验（模块扫描、workspace 根
// 拒绝、默认并发、分析器标记判定）。Build 在 action_build.go。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/ssa"
	"github.com/schaepher/codeintel/internal/orchestrator"
)

// DefaultWorkers 构建默认并发数 = min(NumCPU, 8)（1..8）——
// Q221 实测冷启动 5m16s → 40s（峰值 RSS ~2.9G）；上限 8 防大机器内存
// 翻倍。小内存机器可显式调小。
func DefaultWorkers() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// DiscoverRepository 解析仓库的 module 信息（P2-3 多 go.mod monorepo）：
// 递归扫描根下所有 go.mod，构造 Repository（根 module 在前）。
// ignoreDirs：忽略目录（不进入/不作为目标 module；写入 Repository 供
// 加载/适配器过滤）。
func DiscoverRepository(repoPath string, ignoreDirs ...string) (*domain.Repository, error) {
	if len(ignoreDirs) == 0 {
		// 未显式传入 → 读仓库级配置（codeintel.yaml 的 ignore_dirs）
		ignoreDirs = LoadIgnoreDirs(repoPath)
	}
	modules, dirs, err := orchestrator.DiscoverModules(repoPath, ignoreDirs)
	if err != nil {
		return nil, err
	}
	return &domain.Repository{
		Path:       repoPath,
		Module:     modules[0],
		Modules:    modules,
		ModuleDirs: dirs,
		IgnoreDirs: ignoreDirs,
	}, nil
}

// ValidateBuildTarget 构建前校验目标目录：必须是目录；workspace 根
// （有 go.work 无 go.mod）拒绝并提示进入模块目录（field_trace.md §1.3）。
//
// cli 在打印构建头部（module/忽略目录）前也调用它——错误顺序与提示
// 与"直接构建"一致。
func ValidateBuildTarget(abs string) error {
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", abs)
	}
	if w := findGoWork(abs); w != "" {
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
			return fmt.Errorf("检测到 go.work（%s）。请进入具体模块目录后运行：codeintel init --repo <模块目录>", w)
		}
	}
	return nil
}

// AnalyzerVersionChanged 分析器版本标记与当前二进制是否不一致
// （不一致时增量会降级全量——调用方据此提示用户）。
func AnalyzerVersionChanged(repoPath string) bool {
	return ssa.LoadAnalyzerMarker(repoPath) != ssa.AnalyzerVersionHash()
}

// findGoWork 从目录向上（最多 4 层）查找 go.work；未找到返回空串。
func findGoWork(dir string) string {
	for i := 0; i < 4 && dir != "" && dir != "/"; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return filepath.Join(dir, "go.work")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
