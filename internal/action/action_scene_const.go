package action

// todo22 P1：场景判定的常量表——当前包常量 + **跨包常量**（otherpkg.Const）。
//
// 严格边界（与 §5 一致）：值只来自真实 const 声明（含 `Source(7)` 转换），
// 不做"按名称猜值"；跨包常量按调用文件里的 import 别名反查 import path，
// 用 `go list -e -json -find -mod=readonly`（离线只读）定位包目录后解析
// 其 const 声明——解析不到就是"不是常量"，条件保持 unknown。
//
// 性能：跨包解析是**按需**的（只在比较表达式真的引用 `alias.Name` 时触发），
// 且按 import path 在请求内缓存；没有跨包常量引用的查询多付 0 次 go list。

import (
	"go/ast"
	"go/token"
	"strings"
)

// seqMaxConstDepth 跨包常量解析深度上限（防 import 环/异常嵌套）。
const seqMaxConstDepth = 4

// sceneConstLookup 场景判定用的常量查询接口。
type sceneConstLookup interface {
	constValue(name string) (string, bool)
}

// sceneConstsMap 纯 map 实现（单测/无请求上下文时使用）。
type sceneConstsMap map[string]string

func (m sceneConstsMap) constValue(name string) (string, bool) {
	v, ok := m[name]
	return v, ok
}

// sceneConstValue nil 安全的常量查询。
func sceneConstValue(l sceneConstLookup, name string) (string, bool) {
	if l == nil {
		return "", false
	}
	return l.constValue(name)
}

// seqConstTable 场景常量表：当前包常量（vals）+ 跨包按需解析。
type seqConstTable struct {
	vals  map[string]string
	a     *Actions
	req   CodeSequenceRequest
	ctx   *seqFileCtx // 引用常量表达式的文件（决定 import 别名）
	depth int
}

func (t *seqConstTable) constValue(name string) (string, bool) {
	if t == nil || t.vals == nil || name == "" {
		return "", false
	}
	if v, ok := t.vals[name]; ok {
		return v, true
	}
	// 跨包常量：alias.Name / pkg.Name
	alias, cname, ok := strings.Cut(name, ".")
	if !ok || alias == "" || cname == "" || strings.Contains(cname, ".") {
		return "", false
	}
	if t.ctx == nil || t.ctx.file == nil || t.a == nil {
		return "", false
	}
	importPath := seqImportPath(t.ctx.file, alias)
	if importPath == "" {
		return "", false
	}
	v, ok := seqPkgConstValue(t.a, t.req, importPath, cname, t.depth+1)
	if !ok {
		return "", false
	}
	t.vals[name] = v
	return v, true
}

// seqPkgConstValue 跨包常量值（import path + 常量名；请求内缓存包常量表）。
func seqPkgConstValue(a *Actions, req CodeSequenceRequest, importPath, name string, depth int) (string, bool) {
	tbl := seqPkgConstTable(a, req, importPath, depth)
	if tbl == nil {
		return "", false
	}
	v, ok := tbl[name]
	return v, ok
}

// seqPkgConstTable 一个包的全部 const（name → 值文本）。
// 值不可判定的常量不写入（保持"不是常量证据"），避免把未知当值。
func seqPkgConstTable(a *Actions, req CodeSequenceRequest, importPath string, depth int) map[string]string {
	if depth > seqMaxConstDepth || req.constPkgs == nil {
		return nil
	}
	if tbl, ok := req.constPkgs[importPath]; ok {
		return tbl
	}
	pkg := seqExternalPackage(a, req, importPath)
	if pkg == nil || pkg.err != nil || pkg.std || len(pkg.files) == 0 {
		req.constPkgs[importPath] = nil // 未命中同样缓存（不重复 go list）
		return nil
	}
	vals := map[string]string{}
	// 先占位：包内常量可能互相引用（含前向引用），迭代解析时复用同一张表
	req.constPkgs[importPath] = vals
	type constSpec struct {
		name string
		expr ast.Expr
		ctx  *seqFileCtx
	}
	var specs []constSpec
	for _, c := range pkg.files {
		for _, decl := range c.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, nm := range vs.Names {
					if i >= len(vs.Values) || !ast.IsExported(nm.Name) {
						continue
					}
					specs = append(specs, constSpec{name: nm.Name, expr: vs.Values[i], ctx: c})
				}
			}
		}
	}
	// 迭代解析（3 轮足够处理同一清单里的前向/跨常量引用；仍不可判定则留空）
	for round := 0; round < 3; round++ {
		changed := false
		for _, sp := range specs {
			if _, ok := vals[sp.name]; ok {
				continue
			}
			t := &seqConstTable{vals: vals, a: a, req: req, ctx: sp.ctx, depth: depth}
			if v, ok := sceneValueOf(sp.expr, t); ok {
				vals[sp.name] = v
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return vals
}
