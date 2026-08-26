package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/ssa"
	"github.com/schaepher/codeintel/internal/logging"
)

// pkgPatterns 增量分析的包 pattern（key = module 目录相对仓库根，
// value = 该 module 下相对目录 pattern，如 "./pkg/app/daemon"）。
type pkgPatterns map[string][]string

// IncrementalBuild 增量构建（TD.md 5.2 增量语义，MVP：全量分析 + 增量写入）：
//  1. 删除变更文件旧数据（节点级联删边与摘要）
//  2. 适配器全量运行，写库时只保留与变更文件相关的产出
//     （节点 file_path ∈ 变更文件；边/摘要的端点属于变更文件）
//  3. build_metadata 记录 tool_name=incremental
//
// 语义正确性（R84）：分析按包增量——只 Load/分析变更文件所在包（依赖
// 包走 go/packages fast 模式 export data，AST 跨包调用用 types 信息
// 构造 target，不依赖被调包 AST），未变更包数据原样复用库中已有索引；
// SSA 只构建传入包主体，全程序扫描（dispatch/别名/动态 SQL）随分析
// 范围收缩——与 keep 写库过滤互补（非变更包产出本来就不写库）。
// 跨包数据流闭包（变更包函数经由未变更包函数的字段追溯）增量时在
// 变更包处截断（Q182 marker 版本变化 / go.mod 变更仍降级全量）。
func (o *Orchestrator) IncrementalBuild(ctx context.Context, changedFiles []string) (*BuildResult, error) {
	logger := logging.FromContext(ctx)
	logger.Debug("enter (Orchestrator).IncrementalBuild")
	defer logger.Debug("exit (Orchestrator).IncrementalBuild")
	start := time.Now()

	if ssa.LoadAnalyzerMarker(o.Repo.Path) != ssa.AnalyzerVersionHash() {
		logger.Info("分析器版本变化，增量更新降级为全量重建（新特性须全量生效）")
		return o.FullBuild(ctx)
	}

	changed := map[string]bool{}
	for _, f := range changedFiles {
		changed[f] = true
	}
	if err := deleteFiles(o.RepoImpl, changedFiles); err != nil {
		return nil, fmt.Errorf("delete changed files: %w", err)
	}

	endpointFile := map[string]string{}
	endpointInChanged := func(id string) bool {
		if fp, ok := endpointFile[id]; ok {
			return changed[fp]
		}
		var fp sql.NullString
		if err := o.RepoImpl.QueryRow("SELECT file_path FROM nodes WHERE id = ?", id).Scan(&fp); err != nil || !fp.Valid {
			return true
		}
		endpointFile[id] = fp.String
		return changed[fp.String]
	}
	keep := func(item domain.Item) bool {
		switch {
		case item.Node != nil:
			return changed[item.Node.FilePath]
		case item.Fact != nil:
			return endpointInChanged(string(item.Fact.SourceID)) || endpointInChanged(string(item.Fact.TargetID))
		case item.Summary != nil:
			return endpointInChanged(string(item.Summary.FunctionID))
		}
		return false
	}

	patterns, full := o.changedPackagePatterns(changedFiles)
	if full {
		patterns = nil
	}
	pkgs, err := o.loadPackages(ctx, patterns)
	if err != nil {
		return nil, err
	}
	results, skipped, err := o.runAdapters(ctx, pkgs, keep, changedFiles)
	if err != nil {
		return nil, err
	}
	return o.finishBuild(start, results, skipped, "incremental")
}

// changedPackagePatterns 变更文件 → 变更包 patterns（R84 按包增量）：
//   - .go 文件 → 所属 module 目录下 filepath.Dir 的 pattern（"./dir"）
//   - 包目录已不存在（包删除）→ 跳过（旧数据已被 deleteFiles 清理）
//   - go.mod/go.work 或无法定位 module 的文件 → full=true（模块结构
//     变化 / 无法归属——保守全量分析）
func (o *Orchestrator) changedPackagePatterns(changedFiles []string) (pkgPatterns, bool) {
	pats := pkgPatterns{}
	seen := map[string]bool{}
	for _, f := range changedFiles {
		if f == "go.mod" || f == "go.work" || strings.HasPrefix(f, "go.mod/") || strings.HasPrefix(f, "go.work/") {
			return nil, true
		}
		modDir, rel, ok := o.moduleRelDir(f)
		if !ok {
			return nil, true
		}
		p := filepath.ToSlash(filepath.Dir(rel))
		if p == "." {
			p = "./"
		} else {
			p = "./" + p
		}
		key := modDir + "|" + p
		if seen[key] {
			continue
		}
		seen[key] = true

		abs := filepath.Join(o.Repo.Path, filepath.FromSlash(filepath.Join(modDir, strings.TrimPrefix(p, "./"))))
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		pats[modDir] = append(pats[modDir], p)
	}
	return pats, false
}

// moduleRelDir 变更文件（相对仓库根）→ 所属 module 目录（相对仓库根）
// 与该 module 下的相对路径。子 module 前缀优先匹配（先于根 "." 兜底）。
// 无匹配 module 时 ok=false。
func (o *Orchestrator) moduleRelDir(f string) (modDir, rel string, ok bool) {
	for _, md := range o.Repo.ModuleDirs {
		if md == "." {
			continue
		}
		if f == md || strings.HasPrefix(f, md+"/") {
			return md, strings.TrimPrefix(f, md+"/"), true
		}
	}
	return ".", filepath.Clean(f), true
}
