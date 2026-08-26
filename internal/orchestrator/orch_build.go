package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/infrastructure/ssa"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
	"golang.org/x/tools/go/packages"
)

// FullBuild 执行全量构建并返回报告（TD.md 5.2 并行流程）。
func (o *Orchestrator) FullBuild(ctx context.Context) (*BuildResult, error) {
	logger := logging.FromContext(ctx)
	logger.Debug("enter (Orchestrator).FullBuild")
	defer logger.Debug("exit (Orchestrator).FullBuild")
	start := time.Now()

	if err := o.RepoImpl.ResetGraphTables(); err != nil {
		return nil, fmt.Errorf("reset graph tables: %w", err)
	}

	orchestraStart := time.Now()
	orchStage := func(name string) {
		logger.Info("orchestrator stage",
			zap.String("stage", name), zap.Duration("elapsed", time.Since(orchestraStart)))
		orchestraStart = time.Now()
	}
	pkgs, err := o.loadPackages(ctx, nil)
	if err != nil {
		return nil, err
	}
	orchStage("loadPackages")
	results, skipped, err := o.runAdapters(ctx, pkgs, nil, nil)
	if err != nil {
		return nil, err
	}
	orchStage("runAdapters")
	result, err := o.finishBuild(start, results, skipped, "all")
	if err == nil {
		// Q182：全量构建后写全局分析器 marker——后续 update 据此判断
		// 是否需降级全量（新特性/逻辑变化后增量写库范围无法覆盖未变更包）
		if merr := ssa.SaveAnalyzerMarker(o.Repo.Path); merr != nil {
			logger.Warn("save analyzer marker", zap.Error(merr))
		}
	}
	return result, err
}

// loadPackages 统一加载仓库 go/packages（内存优化：AST/SSA 适配器共享
// 一次类型检查，避免各自 Load 翻倍）。返回共享结果供适配器复用。
// R84：patterns 非 nil 时按包增量——只 Load 变更包（pattern 相对各
// module 目录；该 module 无变更则跳过 Load，数据复用库中已有索引）；
// patterns 为 nil 时全量 "./..."。依赖包走 go/packages fast 模式
// （NeedTypes 从 export data 加载——AST 跨包调用用 types 信息构造
// target，不依赖被调包 AST 主体）。
// P2-3 多 go.mod：每个 module 单独 Load（go/packages 不能跨 module），
// 按 PkgPath 去重合并（同一包路径只属于一个 module，Go 语义保证）。
func (o *Orchestrator) loadPackages(ctx context.Context, patterns pkgPatterns) ([]*packages.Package, error) {
	seen := map[string]bool{}
	var out []*packages.Package
	for i, d := range o.Repo.ModuleDirs {
		dir := o.Repo.Path
		if i > 0 {
			dir = filepath.Join(o.Repo.Path, d)
		}
		pat := []string{"./..."}
		if patterns != nil {
			if ps, ok := patterns[d]; ok && len(ps) > 0 {
				pat = ps
			} else {
				continue // 该 module 无变更包——跳过 Load（数据已在库中）
			}
		}
		cfg := &packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax |
				packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
			Dir: dir,
		}
		pkgs, err := packages.Load(cfg, pat...)
		if err != nil {
			return nil, fmt.Errorf("go/packages load (%s): %w", dir, err)
		}
		for _, p := range pkgs {
			if seen[p.PkgPath] {
				continue
			}
			seen[p.PkgPath] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// DiscoverModules 递归扫描仓库根下的 go.mod（跳过 .git/.codeintel/vendor/
// node_modules），返回 module 路径与相对仓库根的目录（根 go.mod 在前）。
// P2-3 多 go.mod monorepo。
func DiscoverModules(repoPath string) (modules []string, dirs []string, err error) {
	rootMod, err := readGoModModule(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return nil, nil, err
	}
	modules = append(modules, rootMod)
	dirs = append(dirs, ".")
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			switch e.Name() {
			case ".git", ".codeintel", "vendor", "node_modules":
				continue
			}
			sub := filepath.Join(dir, e.Name())
			if _, err := os.Stat(filepath.Join(sub, "go.mod")); err == nil {
				m, err := readGoModModule(filepath.Join(sub, "go.mod"))
				if err != nil {
					continue
				}
				rel, _ := filepath.Rel(repoPath, sub)
				modules = append(modules, m)
				dirs = append(dirs, filepath.ToSlash(rel))
				continue
			}
			if err := walk(sub); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(repoPath); err != nil {
		return nil, nil, err
	}
	return modules, dirs, nil
}

// readGoModModule 解析 go.mod 的 module 指令。
func readGoModModule(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			m := strings.TrimSpace(rest)
			if i := strings.Index(m, " "); i >= 0 {
				m = m[:i]
			}
			if m != "" {
				return m, nil
			}
		}
	}
	return "", fmt.Errorf("go.mod 无 module 指令: %s", path)
}

// deleteFiles 删除指定文件的节点（级联删边与摘要行）；分批避免 SQLite
// 参数上限（999）。
func deleteFiles(repo *sqlite.Repo, files []string) error {
	const batchSize = 400
	for i := 0; i < len(files); i += batchSize {
		end := i + batchSize
		if end > len(files) {
			end = len(files)
		}
		batch := files[i:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for j, f := range batch {
			args[j] = f
		}
		if _, err := repo.Exec("DELETE FROM nodes WHERE file_path IN ("+placeholders+")", args...); err != nil {
			return err
		}
	}
	return nil
}
