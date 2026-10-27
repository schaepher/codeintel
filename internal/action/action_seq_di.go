package action

// todo22 §3.3：DI 字段注入的**注册证据**（不含框架特判）。
//
// 严格边界（用户 2026-10-27 确认的风格：证据不足就保持未知）：
//   - 只认**显式注册调用**：`X.Register(<字符串字面量 key>, <impl>)` /
//     `X.Bind(<key>, <impl>)`，且必须出现在 `init()` 或 `Init()`/`Setup()`
//     引导函数里（非 init 注册无法证明执行 → 不算证据）。
//   - 只认**能解析成具体类型**的 impl：`&T{}` / `T{}` / 构造器调用（返回
//     类型可解析）。动态 key、多重注册、条件注册、未知构造器 → 明确 unknown。
//   - 字段 key 来源：字段注释里的注解（`// @inject store` / `//di:store`）
//     优先，其次是 struct tag（`inject:"store"` / `di:"store"`），最后按
//     字段名匹配。
//   - **AOP**：impl 是包装调用（`Wrap(&T{})`）时不直接接业务方法——目标取
//     包装器的返回类型（`rhsTypes`），证据 reason 标 `aop-wrapper`；包装器
//     内部是否调用原实现由正常展开显示，不在这里"跳过"。
//   - 显式字段写入的普通数据流结论优先：调用方先跑 `receiverFieldImpl`，
//     只有它无结论时才用本文件的注册证据。

import (
	"go/ast"
	"strings"
)

// DI 证据来源（evidence.reason / di_registration 字段）。
const (
	seqReasonDIRegistration = "di-field-registration"
	seqReasonDIAnnotation   = "di-field-annotation"
	seqReasonAOPWrapper     = "di-aop-wrapper"
	// todo22 P1：框架式 Bean/Provider 注解（`// @bean key` / `// di:bean key`）
	seqReasonDIBeanAnnotation = "di-bean-annotation"
	SeqEvidenceDI             = "di"
)

// seqDIReg 一条注册证据。
type seqDIReg struct {
	key      string // 注册 key（字符串字面量）
	typeName string // 注册的实现类型名（同包/跨包 pkg.T 的 T）
	pkgPath  string // 实现类型所在包
	file     string // 注册点所在文件（仓库相对）
	line     int    // 注册点行号
	wrapper  bool   // impl 是包装调用（AOP）
	kind     string // register|bind|set|provide|apply|put|bean（契约来源）
}

// extended 非 Register/Bind 的容器/注解契约：必须有**字段注解**才采纳
// （`Set`/`Provide`/`Bean` 这类名字在普通代码里也很常见，避免误配）。
func (r seqDIReg) extended() bool {
	switch r.kind {
	case "", "register", "bind":
		return false
	}
	return true
}

// seqDIImpl 字段注入解析结果。
type seqDIImpl struct {
	symID  string // (Type).Method
	reason string
	reg    seqDIReg
	why    string // 无法解析时的原因（调用方写成 unknown）
}

// diFieldImpl 按字段注解/注册证据解析注入的实现方法。
// 返回 (结果, ok)；ok=false 表示"没有 DI 证据"，调用方继续走既有路径。
func (a *Actions) diFieldImpl(req CodeSequenceRequest, f *ast.File, recvType, field, method string) (seqDIImpl, bool) {
	if field == "" {
		return seqDIImpl{}, false
	}
	key, annReason := diFieldKey(f, recvType, field)
	regs := seqDIRegistrations(a, req)
	pick, ok := diPickRegistration(regs, key, field)
	if !ok {
		// todo22 P1：跨包注册点（引导函数在另一个包）——按索引里的
		// init/Init/Setup/... 函数节点定点解析，不遍历全仓源码。
		regs = seqDIRegistrationsRepo(a, req)
		pick, ok = diPickRegistration(regs, key, field)
	}
	if !ok {
		return seqDIImpl{}, false
	}
	if pick.reg.extended() && annReason == "" {
		// 扩展契约（Set/Provide/Apply/Bean）必须有显式字段注解证据
		return seqDIImpl{}, false
	}
	if pick.why != "" {
		return seqDIImpl{why: pick.why, reason: annReason}, true
	}
	if pick.multi {
		return seqDIImpl{why: "同一 key 有多重注册（无法证明最终绑定）: " + pick.key,
			reason: seqReasonDIRegistration}, true
	}
	reg := pick.reg
	reason := seqReasonDIRegistration
	if annReason != "" {
		reason = annReason
	}
	if reg.kind == "bean" {
		reason = seqReasonDIBeanAnnotation
	}
	if reg.wrapper {
		reason = seqReasonAOPWrapper
	}
	return seqDIImpl{
		symID:  "symbol:go:" + reg.pkgPath + ":(" + reg.typeName + ")." + method,
		reason: reason,
		reg:    reg,
	}, true
}

// seqDIRegistrations 当前包（同目录 .go）里 init()/Init()/Setup() 中的
// 显式注册调用（按 key 归组；扫描结果随 req 缓存）。
func seqDIRegistrations(a *Actions, req CodeSequenceRequest) map[string][]seqDIReg {
	// 哨兵键记录"已扫描"（req 按值传递，空 map 不能当命中标志）
	if req.diRegs != nil {
		if _, ok := req.diRegs[seqDIRegScannedKey]; ok {
			return req.diRegs
		}
	}
	out := map[string][]seqDIReg{}
	files := seqPackageFiles(req)
	for _, rel := range files {
		ctx := seqFileCtxFor(req, rel)
		if ctx == nil {
			continue
		}
		for _, decl := range ctx.file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fd.Body != nil && seqIsBootstrapFunc(fd) {
				seqScanRegistrations(a, req, ctx, fd.Body, out)
			}
		}
		// todo22 P1：Bean/Provider 注解契约（`// @bean store`）——按声明
		// 注释登记（框架容器会在启动时调用该 provider）。
		seqScanBeans(req, ctx, out)
	}
	if req.diRegs != nil {
		for k, v := range out {
			req.diRegs[k] = v
		}
		req.diRegs[seqDIRegScannedKey] = nil
		return req.diRegs
	}
	return out
}

// seqIsBootstrapFunc 注册必须发生在引导函数里（非 init 注册无法证明执行）。
func seqIsBootstrapFunc(fd *ast.FuncDecl) bool {
	if fd.Recv != nil {
		return false
	}
	switch fd.Name.Name {
	case "init", "Init", "Setup", "RegisterDeps", "Bootstrap":
		return true
	}
	return false
}

// seqScanRegistrations 扫描一个引导函数体内的注册调用（顶层语句；条件/
// 循环里的注册不计入——无法证明一定执行）。
func seqScanRegistrations(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	body *ast.BlockStmt, out map[string][]seqDIReg) {
	for _, st := range body.List {
		// 允许 init 里调用 RegisterXxx()（一跳展开到同包函数）
		if es, ok := st.(*ast.ExprStmt); ok {
			if call, ok2 := es.X.(*ast.CallExpr); ok2 {
				seqCollectRegCalls(a, req, ctx, call, out, 0)
			}
		}
	}
}

// seqCollectRegCalls 收集注册调用（含一跳同包 RegisterXxx 展开）。
func seqCollectRegCalls(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	call *ast.CallExpr, out map[string][]seqDIReg, depth int) {
	// 注册函数名：同包函数 `Register(...)` 或容器方法 `c.Register(...)`
	name := ""
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		name = fn.Sel.Name
	case *ast.Ident:
		name = fn.Name
	}
	kind := ""
	switch name {
	case "Register", "Bind":
		kind = strings.ToLower(name)
	case "Set", "Provide", "Apply", "Put":
		// todo22 P1：其它容器的注册形态（gof/自研容器）——需显式字段注解
		kind = strings.ToLower(name)
	}
	if kind == "" {
		// 一跳：init 里调用同包的 RegisterXxx()
		if depth == 0 {
			if id, ok2 := call.Fun.(*ast.Ident); ok2 {
				if fctx, fd, ok3 := loadSeqFuncDecl(a, req, "symbol:go:"+pkgPathOf(req.curSym)+":"+id.Name); ok3 && fd.Body != nil {
					for _, st := range fd.Body.List {
						if es, ok4 := st.(*ast.ExprStmt); ok4 {
							if inner, ok5 := es.X.(*ast.CallExpr); ok5 {
								seqCollectRegCalls(a, req, fctx, inner, out, depth+1)
							}
						}
					}
				}
			}
		}
		return
	}
	if len(call.Args) < 2 {
		return
	}
	keyLit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || keyLit.Kind.String() != "STRING" {
		return // 动态 key：不是证据
	}
	key := strings.Trim(keyLit.Value, "\"`")
	implExpr := call.Args[1]
	wrapper := false
	if inner, ok := implExpr.(*ast.CallExpr); ok {
		// AOP：Wrap(&T{})——实现类型取包装器的返回类型
		wrapper = true
		implExpr = inner
	}
	ref, ok := seqTypeOfExpr(a, req, ctx, implExpr)
	if !ok {
		return
	}
	line := 0
	if ctx.fset != nil {
		line = ctx.fset.Position(call.Lparen).Line
	}
	out[key] = append(out[key], seqDIReg{key: key, typeName: ref.typeName, pkgPath: ref.pkgPath,
		file: ctx.rel, line: line, wrapper: wrapper, kind: kind})
}

// seqDIPick 字段注入的选取结果。
type seqDIPick struct {
	reg   seqDIReg
	key   string
	multi bool
	why   string
}

// diPickRegistration 按注解 key（或字段名）选注册：多重注册 → 未消歧。
func diPickRegistration(regs map[string][]seqDIReg, key, field string) (seqDIPick, bool) {
	for _, k := range []string{key, field} {
		if k == "" {
			continue
		}
		if list, ok := regs[k]; ok {
			switch len(list) {
			case 0:
				continue
			case 1:
				return seqDIPick{reg: list[0], key: k}, true
			default:
				return seqDIPick{key: k, multi: true}, true
			}
		}
	}
	return seqDIPick{}, false
}
