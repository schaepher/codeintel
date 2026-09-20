package orchestrator

import (
	"context"
	"fmt"
	"go/token"
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
	rep := o.prog()
	// orchStage(name)：报告**刚完成**的步骤耗时（语义与 Q253 前一致）。
	orchStage := func(name string) {
		elapsed := time.Since(orchestraStart)
		logger.Info("orchestrator stage",
			zap.String("stage", name), zap.Duration("elapsed", elapsed))
		rep.End(name, elapsed, nil)
		orchestraStart = time.Now()
	}
	rep.Begin("loadPackages", 0, 0)
	pkgs, err := o.loadPackages(ctx, nil)
	if err != nil {
		return nil, err
	}
	orchStage("loadPackages")
	rep.Begin("runAdapters", 0, 0)
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
// patterns 为 nil 时全量 "./..."。
//
// Q247 依赖信息契约（**改 Mode 前必读**）：Mode 不含 `NeedDeps`——
//   - 模块内包：Syntax + TypesInfo + Types 齐全（适配器全量分析前提）
//   - 依赖包：**只保证 Types**（export data），不保证 Syntax/TypesInfo
//     任何适配器不得读非模块包的 Syntax/TypesInfo（现有消费者均只用
//     Types：ast_index 取 Imports 键、ast_emit 查 net/http.Handler 的
//     Types.Scope、ast_grpc_collect 递归 walk 只用 Types（R90 外部注册
//     识别即按 types 设计）、ssa 缓存键取 CompiledGoFiles）。
//     带 NeedDeps 会让 go/packages 把**整个传递依赖图的 AST/TypesInfo
//     也解析并常驻**：go2o 实测 774 个 reachable 包全部带 AST（3718 个
//     语法文件）/ load 后存活堆 955MB → 137 包 526 文件 / 148MB；本仓库
//     1095MB → 55MB（3GB 机器换页的主因，docs/design-q247.md §1）。
//     契约由 TestLoadPackagesDependencyContract 锁定（含外部包 Types
//     可达性回归护栏）。
//
// P2-3 多 go.mod：每个 module 单独 Load（go/packages 不能跨 module），
// 按 PkgPath 去重合并（同一包路径只属于一个 module，Go 语义保证）。
func (o *Orchestrator) loadPackages(ctx context.Context, patterns pkgPatterns) ([]*packages.Package, error) {
	// Q252f：空 ModuleDirs 是**静默退化**阀值——循环体一次不进、返回空包集，
	// 上层却报 success（AST/SSA 什么都没干，只剩余 scip/git 产物；Q246 的
	// benchmark、Q252f 的 e2e 测试都踩过“209 节点/1 边看着像成功”）。宁可
	// 大声报错。
	if len(o.Repo.ModuleDirs) == 0 {
		return nil, fmt.Errorf("loadPackages: Repository.ModuleDirs 为空（modules=%v）——拒绝返回空包集：AST/SSA 适配器会全部空跑而构建仍报 success", o.Repo.Modules)
	}
	seen := map[string]bool{}
	var out []*packages.Package
	// Q251：多 module 共用**一个** token.FileSet——原先每个 module 各一次
	// packages.Load（各自新建 Fset），而 ssautil.Packages 的 Program 只用
	// initial[0].Fset（x/tools/go/ssa/ssautil/load.go）：非首模块的 token.Pos
	// 在错误 Fset 里解析出行号错乱（461 行的文件报 498 行）、relPath 失败
	// 返回空（file_path 丢失 → 增量按文件删除匹配不到）。共享 Fset 后所有
	// 包的位置都在同一坐标系，SSA/AST 两适配器同时受益。
	fset := token.NewFileSet()
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
				packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
			Dir:  dir,
			Fset: fset, // Q251：跨 module 共享
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
	// 全量构建（patterns==nil）却一个包都没加载到 = 配置/路径错，不是“空仓库”
	// ——同样拒绝静默成功（增量构建时 patterns 会让无关 module 跳过，不能报错）。
	if patterns == nil && len(out) == 0 {
		return nil, fmt.Errorf("loadPackages: 未加载到任何 Go 包（path=%s modules=%v dirs=%v）——检查 ModuleDirs 与仓库目录", o.Repo.Path, o.Repo.Modules, o.Repo.ModuleDirs)
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
			case ".git", ".codeintel", "vendor", "node_modules", ".tmp":
				// .tmp：本项目临时目录（R67：TMPDIR=$PWD/.tmp）——
				// 测试/脚本会在里面建临时 Go module，不是待索引目标
				// （否则并发跑测试时 init 会把临时 module 当目标 module）
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
