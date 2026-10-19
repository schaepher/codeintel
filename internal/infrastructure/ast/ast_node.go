package ast

// todo10-1：函数节点构建级缓存（从 ast_resolve.go 拆出——行数治理）。
// 同一函数在大量调用点重复生成节点（signature 渲染 + 属性 map）；缓存
// 只覆盖模块内函数、按包重置，节点只读共享（orchestrator 只比较/序列化）。

import (
	"go/types"

	"github.com/schaepher/codeintel/internal/canonicalizer"
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/packages"
)

// astNodeKey 节点缓存键（todo10-1）：同一函数在大量调用点重复生成节点
// （signature 渲染 + 属性 map）。限定符包决定 signature 的相对渲染结果，
// flags 区分 serves_http/serves_grpc 变体。
type astNodeKey struct {
	fn    *types.Func
	qual  *types.Package
	flags uint8
}

// astNodeFlags 服务标记位（仅这两个已知键可缓存；未知键回退不缓存）。
func astNodeFlags(extra map[string]bool) (uint8, bool) {
	var b uint8
	for k := range extra {
		switch k {
		case "serves_http":
			b |= 1
		case "serves_grpc":
			b |= 2
		default:
			return 0, false
		}
	}
	return b, true
}

// nodeFor 节点缓存（构建级；Index 入口重置——不跨构建持有旧类型对象）。
// 命中的 *CodeEntity 会被多处 emit，只读不修改（orchestrator 只做内容
// 比较/序列化）；内容与未缓存时逐字段一致。
func (a *Adapter) nodeFor(repo *domain.Repository, pkg *packages.Package, fn *types.Func,
	id domain.CanonicalID, kind domain.EntityKind, extra map[string]bool) *domain.CodeEntity {
	flags, cacheable := astNodeFlags(extra)
	// 只缓存模块内函数节点：模块函数的调用点最多（caller 节点 = 每调用点
	// 一次），缓存收益大且节点数受模块函数数约束；外部被调函数按
	// (函数, 调用方包) 变体数量大，长期驻留不划算（峰值 +20MB 实测）。
	if a == nil || fn == nil || pkg == nil || pkg.Types == nil || !cacheable ||
		fn.Pkg() == nil || !isInModule(fn.Pkg().Path(), a.modules) {
		return buildFuncNode(repo, pkg, fn, id, kind, extra)
	}
	key := astNodeKey{fn: fn, qual: pkg.Types, flags: flags}
	a.nodeMu.Lock()
	n, ok := a.nodeCache[key]
	a.nodeMu.Unlock()
	if ok {
		return n
	}
	n = buildFuncNode(repo, pkg, fn, id, kind, extra)
	a.nodeMu.Lock()
	if prev, ok := a.nodeCache[key]; ok {
		a.nodeMu.Unlock()
		return prev
	}
	if a.nodeCache == nil {
		a.nodeCache = map[astNodeKey]*domain.CodeEntity{}
	}
	a.nodeCache[key] = n
	a.nodeMu.Unlock()
	return n
}

// resetNodeCache 清空节点缓存（Index 入口调用）。
func (a *Adapter) resetNodeCache() {
	a.nodeMu.Lock()
	a.nodeCache = nil
	a.nodeMu.Unlock()
}

// buildFuncNode 为函数/方法生成轻量节点（ID 与 SCIP 一致，行号/文件来自位置信息，
// signature 由 go/types 生成，与 SCIP 节点通过 properties 合并）。
func buildFuncNode(repo *domain.Repository, pkg *packages.Package, fn *types.Func, id domain.CanonicalID,
	kind domain.EntityKind, extra map[string]bool) *domain.CodeEntity {
	logger := zap.L()
	logger.Debug("enter nodeFor")
	defer logger.Debug("exit nodeFor")
	n := &domain.CodeEntity{ID: id, Kind: kind}
	if fn != nil && fn.Pkg() != nil {
		pos := pkg.Fset.PositionFor(fn.Pos(), false)
		n.FilePath = relPath(repo.Path, pos.Filename)
		n.LineStart = pos.Line
		n.LineEnd = pos.Line

		if kind == domain.KindMethod {
			sig, _ := fn.Type().(*types.Signature)
			if sig != nil && sig.Recv() != nil {
				t := sig.Recv().Type()
				if p, ok := t.(*types.Pointer); ok {
					t = p.Elem()
				}
				if named, ok := t.(*types.Named); ok {
					n.Name = canonicalizer.MethodName(named.Obj().Name(), fn.Name())
				}
			}
		} else {
			n.Name = fn.Name()
		}
		n.Properties = map[string]any{

			"signature": types.ObjectString(fn, types.RelativeTo(pkg.Types)),
		}
		for flag := range extra {
			n.Properties[flag] = "true"
		}
	}
	return n
}
