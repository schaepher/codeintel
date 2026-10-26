package action

// todo20-9：调用点 → 被调符号映射。索引 calls 边按 (caller,target,kind)
// 汇总，重复调用只有首个位置带 pos/line metadata——byName 保存本函数
// 目标集合，按调用名/包路径/函数-方法类别唯一匹配后回退。

import (
	"go/ast"
	"go/token"
	"strings"
)

// seqTargets 时序图调用点 → 被调符号映射（R99-3——用户检查发现：
// 原 map[int]string 按行号，同一行多个调用时后写覆盖先写，两个
// 时序节点都拿到同一个 target）。byPos 按字节 offset（发射端
// metadata pos——同行各调用独立，精确匹配）；byLine 按行号
// （兼容无 pos 的旧索引——同行覆盖为旧行为，仅 fallback）。
type seqTargets struct {
	byPos  map[int]seqTarget
	byLine map[int]seqTarget
	// byName 本次函数调用的全部目标（按符号短名索引）——重复调用点的
	// 边被合并后靠它恢复后续调用点的目标身份（todo20-9）。
	byName map[string][]seqTarget
}

// seqTarget 调用点对应的被调符号 + 边置信度（todo10-3 证据用）。
type seqTarget struct {
	tid        string
	confidence float64
	// aggregated 目标来自同函数汇总边的名称回退（不是该位置的 pos
	// 直接命中）——证据只能标 corroborated（todo20-9）。
	aggregated bool
}

// lookup 查调用位置对应的被调符号：offset 优先，行号 fallback。
func (t *seqTargets) lookup(pos token.Position) (seqTarget, bool) {
	if t.byPos != nil {
		if tg, ok := t.byPos[pos.Offset]; ok {
			return tg, true
		}
	}
	if t.byLine != nil {
		if tg, ok := t.byLine[pos.Line]; ok {
			return tg, true
		}
	}
	return seqTarget{}, false
}

// matchAggregated 位置未命中时按"本次函数调用的目标集合 + 调用名"回退
// （todo20-9）。索引 calls 边按 (caller,target,kind) 汇总，重复调用只有
// 首个位置的 metadata；不能因此把后续调用点判成 unresolved。
// 只有过滤后**唯一**候选才回退——同名多目标保持未解析，不猜。
func (t *seqTargets) matchAggregated(fun ast.Expr, f *ast.File, curPkg, recvType string, recvDecl map[string]string) (seqTarget, bool) {
	name, pkgHint, wantMethod, kindKnown := seqCallSiteIdentity(fun, f, curPkg, recvType, recvDecl)
	if name == "" {
		return seqTarget{}, false
	}
	var matched []seqTarget
	for _, tg := range t.byName[name] {
		pkg, _, method := seqSplitTarget(tg.tid)
		if pkgHint != "" && pkg != pkgHint {
			continue
		}
		if kindKnown && method != wantMethod {
			continue
		}
		matched = append(matched, tg)
	}
	if len(matched) != 1 {
		return seqTarget{}, false
	}
	tg := matched[0]
	tg.aggregated = true
	return tg, true
}

// seqCallSiteIdentity 调用表达式的静态身份：(短名, 包路径提示, 是否方法,
// 是否已知函数/方法类别)。包提示只用于排除同名跨包目标；未知时留空。
func seqCallSiteIdentity(fun ast.Expr, f *ast.File, curPkg, recvType string, recvDecl map[string]string) (name, pkg string, method, kindKnown bool) {
	switch x := fun.(type) {
	case *ast.Ident:
		// 同包函数（含递归调用）
		return x.Name, curPkg, false, true
	case *ast.SelectorExpr:
		name = x.Sel.Name
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return name, "", true, false
		}
		if path := seqImportPathOfName(f, id.Name); path != "" {
			return name, path, false, true
		}
		if recvType != "" && id.Name == recvType {
			return name, curPkg, true, true
		}
		if _, ok := recvDecl[id.Name]; ok {
			return name, "", true, true
		}
		return name, "", true, false
	}
	return "", "", false, false
}

// seqImportPathOfName 导入名（默认包名或显式别名）→ 导入路径。
func seqImportPathOfName(f *ast.File, name string) string {
	if f == nil || name == "" {
		return ""
	}
	for _, im := range f.Imports {
		path := strings.Trim(im.Path.Value, "\"")
		if path == "" {
			continue
		}
		local := path
		if i := strings.LastIndex(local, "/"); i >= 0 {
			local = local[i+1:]
		}
		if im.Name != nil {
			local = im.Name.Name
		}
		if local == name {
			return path
		}
	}
	return ""
}

// seqTargetName 被调符号短名（函数=名字；方法=方法名）。
func seqTargetName(id string) string {
	_, name, _ := seqSplitTarget(id)
	return name
}

// seqSplitTarget canonical ID → (包路径, 短名, 是否方法)。
func seqSplitTarget(id string) (pkg, name string, method bool) {
	s := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.Index(s, ":("); i >= 0 {
		pkg = s[:i]
		rest := s[i+2:]
		if j := strings.Index(rest, ")."); j >= 0 {
			name = rest[j+2:]
		}
		return pkg, name, true
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:], false
	}
	return "", "", false
}
