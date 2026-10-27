package action

// todo23 §6：无图节点/无文件位置时的源码定位（仓库内 / 依赖模块），
// 从 action_symbol_source.go 拆出（行数治理）。
//
// 定位规则：`go list -e -json -find -mod=readonly`（离线只读，不下载、
// 不改 go.mod）；仓库内 → location=repository + 相对 file；仓库外
// （replace/模块缓存）→ location=dependency + 绝对 file。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go/parser"
	"go/token"
)

func (a *Actions) symbolSourceFromPackage(req SymbolSourceRequest, importPath, name, indexStatus, repoReal string,
	nodeExists, noFile bool) (*SymbolSource, error) {
	dir, std, lerr := seqGoListPkg(context.Background(), req.RepoAbs, importPath)
	if lerr != nil {
		if !nodeExists {
			return nil, fmt.Errorf("符号不存在（索引中无该 canonical ID）且包源码不可定位: %s: %v", req.SymbolID, lerr)
		}
		return nil, fmt.Errorf("符号没有文件路径（虚拟节点），且包源码不可定位: %s", req.SymbolID)
	}
	if std {
		return nil, fmt.Errorf("标准库声明不提供源码输出（无支持的回退）: %s", req.SymbolID)
	}
	location := "dependency"
	fileBase := dir
	if rel, rerr := filepath.Rel(repoReal, dir); rerr == nil && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		location = "repository"
		fileBase = repoReal
	}
	entries, derr := os.ReadDir(dir)
	if derr != nil {
		return nil, fmt.Errorf("读取包目录失败: %s: %w", dir, derr)
	}
	var hits []*symbolSourceDecl
	var hitSrc []byte
	var hitLabel string
	for _, ent := range entries {
		fn := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(fn, ".go") || strings.HasSuffix(fn, "_test.go") {
			continue
		}
		abs := filepath.Join(dir, fn)
		src, rerr := os.ReadFile(abs)
		if rerr != nil {
			continue
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, abs, src, parser.ParseComments)
		if perr != nil {
			continue
		}
		label := abs
		if location == "repository" {
			if rel, rerr := filepath.Rel(fileBase, abs); rerr == nil {
				label = filepath.ToSlash(rel)
			}
		}
		d, merr := matchAnySymbolSourceDecl(fset, f, name, label)
		if merr != nil {
			if strings.Contains(merr.Error(), "多重声明") {
				return nil, merr
			}
			continue
		}
		hits = append(hits, d)
		hitSrc = src
		hitLabel = label
	}
	switch len(hits) {
	case 0:
		if !nodeExists {
			return nil, fmt.Errorf("符号不存在（索引中无该 canonical ID）: %s", req.SymbolID)
		}
		if noFile {
			return nil, fmt.Errorf("符号没有文件路径（虚拟节点），且包内找不到声明: %s", req.SymbolID)
		}
		return nil, fmt.Errorf("包内找不到匹配的声明: %s（%s）", name, importPath)
	case 1:
		return buildSymbolSource(req.SymbolID, hits[0], hitLabel, location, indexStatus, hitSrc)
	default:
		return nil, fmt.Errorf("包内有多重声明（%d 处同名）: %s（%s）", len(hits), name, importPath)
	}
}
