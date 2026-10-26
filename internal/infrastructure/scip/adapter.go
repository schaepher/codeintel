// Package scip 实现 SCIP 适配器（TD.md 5.1：符号权威，置信度 1.0）。
// 调用 scip-go 生成 SCIP 索引（protobuf），解析后产出：
//   - 符号定义节点（function/method/struct/interface/package/file）
//   - IMPLEMENTS 边（来自 SymbolInformation.Relationships 的 is_implementation）
//
// 已知限制：scip-go 的定义 occurrence 只覆盖符号名本身（不含函数体），
// 引用 occurrence 无法归属到引用者符号，故 REFERENCES 边由 AST 分析补齐。
package scip

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/logging"
	"github.com/scip-code/scip/bindings/go/scip"
	"go.uber.org/zap"
	"golang.org/x/tools/go/packages"
)

// Adapter 是 SCIP 索引适配器。
type Adapter struct {
	BinPath string // scip-go 二进制路径，空则自动查找
	prog    domain.Progress

	// P1-12：增量构建按变更包传 package-patterns（key = module 相对目录，
	// 根为 "."；value = 该 module 下的包 pattern）。空 = 全量索引整个 module。
	patterns map[string][]string
	// P1-08：分析范围内容指纹（orchestrator 注入，与 SSA 包缓存同一口径）。
	// 非空且无 patterns 时启用 SCIP 产物缓存（跳过子进程 + protobuf 解码）。
	contentKey string
}

// SetPackagePatterns 注入增量构建的包 pattern（P1-12：update 只索引变更包）。
// 传入空/nil = 全量索引（init/reindex）。
func (a *Adapter) SetPackagePatterns(p map[string][]string) {
	if a == nil {
		return
	}
	a.patterns = p
}

// SetContentFingerprint 注入分析范围内容指纹（P1-08：全量构建的 SCIP
// 产物缓存键）。空 = 禁用缓存。
func (a *Adapter) SetContentFingerprint(fp string) {
	if a == nil {
		return
	}
	a.contentKey = fp
}

// SetProgress 注入进度上报（orchestrator 转发）。
func (a *Adapter) SetProgress(p domain.Progress) { a.prog = p }

// progress 当前进度实现（未注入 → 静默）。
func (a *Adapter) progress() domain.Progress {
	if a != nil && a.prog != nil {
		return a.prog
	}
	return domain.NopProgress{}
}

var _ domain.IndexerPort = (*Adapter)(nil)

// Name 实现 IndexerPort。
func (a *Adapter) Name() string {
	logger := zap.L()
	logger.Debug("enter (Adapter).Name")
	defer logger.Debug("exit (Adapter).Name")
	return "scip"
}

// Index 在仓库上执行全量 SCIP 索引并流式产出节点与边。
// P2-3 多 go.mod：每个 module 单独跑 scip-go（scip-go 一次索引一个
// module；在各自目录执行，输出独立 .scip 文件）。
func (a *Adapter) Index(ctx context.Context, repo *domain.Repository, _ []*packages.Package, emit domain.EmitFunc) (err error) {
	logger := logging.FromContext(ctx)
	logger.Debug("enter (Adapter).Index")
	defer logger.Debug("exit (Adapter).Index")
	bin, err := a.resolveBin()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(repo.Path, ".codeintel"), 0o755); err != nil {
		return fmt.Errorf("create .codeintel: %w", err)
	}
	// Q251：每个 module 带着"相对仓库根的目录"一起处理——scip-go 在
	// module 目录下运行，document 的 RelativePath 是**模块相对**路径，
	// 直接用会让嵌套 module 的 file_path 缺前缀（rename.go 而不是
	// skills/.../rename.go），file: 节点 ID 还会跨模块碰撞、
	// DeleteByFile 也匹配不到（增量陈旧节点删不掉）。
	type moduleDir struct{ abs, rel string }
	dirs := []moduleDir{{abs: repo.Path, rel: "."}}
	for i, d := range repo.ModuleDirs {
		if i == 0 {
			continue
		}
		dirs = append(dirs, moduleDir{abs: filepath.Join(repo.Path, d), rel: filepath.ToSlash(d)})
	}

	// 进度：每个 module 一次 scip-go（真实分母）。
	p := a.progress()
	p.Begin("scip index", 2, len(dirs))
	scipStart := time.Now()
	defer func() { p.End("scip index", time.Since(scipStart), err) }()

	patterns := a.patterns
	if len(patterns) > 0 && os.Getenv("CODEINTEL_SCIP_PATTERNS") == "off" {
		// 诊断/对照逃生口（P1-12 A/B 验证用）：强制全量索引。
		logger.Info("scip package patterns disabled by CODEINTEL_SCIP_PATTERNS=off")
		patterns = nil
	}
	if len(patterns) > 0 {
		logger.Info("scip incremental patterns", zap.Any("patterns", patterns))
	}

	// P1-08：全量构建（无 patterns）才启用产物缓存：键 = 内容指纹 +
	// scip-go 版本 + 模块目录集合；命中则直接重放（不下发子进程、不
	// 解 protobuf）。
	var (
		cachePath string
		cacheKey  string
		scipVer   string
		buffered  []artifactItem
	)
	if len(patterns) == 0 && a.contentKey != "" {
		moduleRels := make([]string, 0, len(dirs))
		for _, md := range dirs {
			moduleRels = append(moduleRels, md.rel)
		}
		if ver, verr := scipGoVersion(ctx, bin); verr != nil {
			logger.Warn("scip-go version probe failed; artifact cache disabled", zap.Error(verr))
		} else {
			scipVer = ver
			cacheKey = scipCacheKey(a.contentKey, scipVer, moduleRels)
			cachePath = scipCachePath(repo.Path)
			if items, ok := loadArtifacts(cachePath, cacheKey, scipVer); ok {
				for i, it := range items {
					if i%256 == 0 {
						if err := ctx.Err(); err != nil {
							return err
						}
					}
					if err := emit(domain.Item{Node: it.Node, Fact: it.Fact}); err != nil {
						return err
					}
				}
				p.Advance("scip index", len(dirs))
				logger.Info("scip artifact cache hit", zap.Int("items", len(items)),
					zap.String("scip", scipVer))
				return nil
			}
		}
	}

	for i, md := range dirs {
		dir := md.abs
		indexPath := filepath.Join(repo.Path, ".codeintel", fmt.Sprintf("index-%d.scip", i))
		// --skip-tests：不索引 _test.go 测试文件（测试符号不入图）。
		// P1-12：增量构建把变更包 patterns 追加到命令尾部（scip-go
		// 的 package-patterns 位置参数）——子进程仍会 go list -deps，
		// 但类型检查与"访问项目文件"只覆盖目标包集合。
		cmd := exec.CommandContext(ctx, bin, indexArgs(indexPath, patterns[md.rel])...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("scip-go index failed (%s): %v: %s", dir, err, string(out))
		}
		// todo5-1：逐文档解码（不再一次物化全部 Document/Occurrence）；
		// todo9-3：从文件流式读取（不再 os.ReadFile 整份 index）；
		// 损坏的尾部返回失败（不提交部分成功构建）。
		emitFn := emit
		if cachePath != "" {
			// P1-08：按原发射顺序缓冲（只存指针，不复制对象），成功后
			// 整份写缓存——失败/取消不写缓存（不留半份）。
			emitFn = func(item domain.Item) error {
				if item.Node != nil || item.Fact != nil {
					buffered = append(buffered, artifactItem{Node: item.Node, Fact: item.Fact})
				}
				return emit(item)
			}
		}
		err := forEachIndexFileDocument(ctx, indexPath, func(doc *scip.Document) error {
			return a.processDocument(repo, doc, md.rel, emitFn)
		})
		os.Remove(indexPath)
		if err != nil {
			return fmt.Errorf("parse scip index: %w", err)
		}
		p.Advance("scip index", i+1)
	}
	if cachePath != "" {
		if serr := saveArtifacts(cachePath, cacheKey, scipVer, buffered); serr != nil {
			logger.Warn("save scip artifact cache failed", zap.Error(serr))
		} else {
			logger.Info("scip artifact cache saved", zap.Int("items", len(buffered)),
				zap.String("scip", scipVer))
		}
	}
	return nil
}

func (a *Adapter) resolveBin() (string, error) {
	logger := zap.L()
	logger.Debug("enter (Adapter).resolveBin")
	defer logger.Debug("exit (Adapter).resolveBin")
	if a.BinPath != "" {
		return a.BinPath, nil
	}
	if p, err := exec.LookPath("scip-go"); err == nil {
		return p, nil
	}
	// 兜底：go env GOBIN / GOPATH/bin（go install 的默认位置）
	for _, envVar := range []string{"GOBIN", "GOPATH"} {
		out, err := exec.Command("go", "env", envVar).Output()
		if err != nil {
			continue
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			continue
		}
		p := filepath.Join(trimmed, "bin", "scip-go")
		if envVar == "GOBIN" {
			p = filepath.Join(trimmed, "scip-go")
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("scip-go not found (install with: go install github.com/scip-code/scip-go/cmd/scip-go@latest)")
}
