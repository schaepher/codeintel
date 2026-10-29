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
	// T08：按文件解析 const 声明（含 iota / 省略表达式）；跨包/前向引用在
	// 3 轮内收敛（每轮复用同一张表）。
	for round := 0; round < 3; round++ {
		changed := false
		for _, c := range pkg.files {
			for _, decl := range c.file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				t := &seqConstTable{vals: vals, a: a, req: req, ctx: c, depth: depth}
				before := len(vals)
				seqConstSpecValuesExported(gd, vals, t)
				if len(vals) != before {
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return vals
}

// seqLocalConstValues 函数体内局部 const 声明（name → 值文本）。只处理同一
// 作用域/嵌套块里的普通 const；iota 等无法静态求值的保持缺省（unknown）。
func seqLocalConstValues(body *ast.BlockStmt, consts sceneConstLookup) map[string]string {
	out := map[string]string{}
	if body == nil {
		return out
	}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		ast.Inspect(n, func(nd ast.Node) bool {
			gd, ok := nd.(*ast.GenDecl)
			if !ok {
				return true
			}
			if gd.Tok != token.CONST {
				return true
			}
			// T08：局部 const 同样支持 iota 与省略表达式。
			seqConstSpecValues(gd, out, consts, false)
			return false
		})
	}
	walk(body)
	return out
}

// sceneConstsOverlay 常量查表叠加（T08：`iota` 是 const 块内的隐式值）。
type sceneConstsOverlay struct {
	base  sceneConstLookup
	extra map[string]string
}

func (o sceneConstsOverlay) constValue(name string) (string, bool) {
	if o.extra != nil {
		if v, ok := o.extra[name]; ok {
			return v, true
		}
	}
	if o.base == nil {
		return "", false
	}
	return o.base.constValue(name)
}

// seqConstSpecValues 一个 const GenDecl 的 (name → 值)：处理 iota 与"省略
// 表达式重复上一行"的 Go 语义（T08：同包/跨包常量表的统一实现）。
func seqConstSpecValues(gd *ast.GenDecl, consts map[string]string, base sceneConstLookup, exportedOnly bool) {
	if gd == nil {
		return
	}
	var prev []ast.Expr
	iota := 0
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		exprs := vs.Values
		if len(exprs) == 0 {
			exprs = prev // 省略表达式：重复上一行（iota 递增）
		} else {
			prev = exprs
		}
		lookup := sceneConstsOverlay{base: base, extra: map[string]string{"iota": itoaSeq(iota)}}
		for i, name := range vs.Names {
			if i >= len(exprs) || name.Name == "_" {
				continue
			}
			if exportedOnly && !ast.IsExported(name.Name) {
				continue
			}
			if v, ok := sceneValueOf(exprs[i], lookup); ok {
				consts[name.Name] = v
			}
		}
		iota++
	}
}

// seqConstSpecValuesExported 跨包常量表：只登记导出常量（调用方只能引用导出）。
func seqConstSpecValuesExported(gd *ast.GenDecl, consts map[string]string, base sceneConstLookup) {
	seqConstSpecValues(gd, consts, base, true)
}
