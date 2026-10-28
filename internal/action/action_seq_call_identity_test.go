package action

// todo24 §11.6：汇总调用边不得混用实例方法与包函数。
//
// 规则：非包 selector（局部/参数/嵌套 receiver）即使 receiver 类型未知，也不
// 得按同名借用独立包函数；导入别名判断前先核对 AST 局部对象（遮蔽场景），
// 嵌套 receiver 同样执行类别检查；合法的重复包函数调用仍要能恢复身份。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

const callIdentitySrc = `package m

import logger "example.com/flow/log"

func Entry(err error) {
	logger.Error("first")
	consume(err.Error())
	logger.Error("repeat")
}

func consume(string) {}

type Worker struct{}

func (*Worker) Error() string { return "worker" }

func MethodEntry(w *Worker) { w.Error() }

func ShadowEntry(w *Worker) { logger := w; logger.Error() }

func NestedEntry(w *Worker) {
	box := struct{ worker *Worker }{w}
	box.worker.Error()
}
`

func parseCallIdentityFixture(t *testing.T) (*token.FileSet, *ast.File, map[string][]*ast.CallExpr) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "flow.go", callIdentitySrc, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string][]*ast.CallExpr{}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		var list []*ast.CallExpr
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				list = append(list, c)
			}
			return true
		})
		calls[fd.Name.Name] = list
	}
	return fset, f, calls
}

func callIdentityTargets() *seqTargets {
	return &seqTargets{byName: map[string][]seqTarget{
		"Error": {
			{tid: "symbol:go:example.com/flow/log:Error"},
			{tid: "symbol:go:example.com/flow:(Worker).Error"},
		},
	}}
}

func findSelectorCall(t *testing.T, calls []*ast.CallExpr, name string) *ast.CallExpr {
	t.Helper()
	for _, c := range calls {
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			return c
		}
	}
	t.Fatalf("夹具里找不到 %s 调用", name)
	return nil
}

func TestCallIdentityPackageVsMethod(t *testing.T) {
	fset, f, calls := parseCallIdentityFixture(t)
	tgts := callIdentityTargets()
	curPkg := "example.com/flow"

	// 1) 合法包函数调用（导入别名）→ 恢复包函数身份。
	pkgCall := findSelectorCall(t, calls["Entry"], "Error")
	tg, ok := tgts.lookupCall(fset.Position(pkgCall.Lparen), pkgCall.Fun, f, curPkg, "", "", nil)
	if !ok || tg.tid != "symbol:go:example.com/flow/log:Error" {
		t.Errorf("logger.Error 应命中 log:Error，got %+v ok=%v", tg, ok)
	}

	// 2) 嵌套实参 err.Error()（未知接口实现）→ 不得借用同名包函数。
	var errCall *ast.CallExpr
	for _, c := range calls["Entry"] {
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && sel.X.(*ast.Ident).Name == "err" {
			errCall = c
		}
	}
	if errCall == nil {
		t.Fatal("夹具缺少 err.Error()")
	}
	// 真实索引里 Entry 的目标集合只有 logger.Error（error.Error 是接口方法、
	// 不建节点/边）——这里按同形构造：只剩包函数候选时，实例 selector 必须
	// 未解析，不得借用包函数。
	onlyPkg := &seqTargets{byName: map[string][]seqTarget{
		"Error": {{tid: "symbol:go:example.com/flow/log:Error"}},
	}}
	if tg, ok := onlyPkg.lookupCall(fset.Position(errCall.Lparen), errCall.Fun, f, curPkg, "", "", nil); ok {
		t.Errorf("err.Error() 不得借用同名包函数，got %+v", tg)
	}

	// 3) 当前 receiver（已知类型）→ 方法身份。
	recvCall := findSelectorCall(t, calls["MethodEntry"], "Error")
	tg, ok = tgts.lookupCall(fset.Position(recvCall.Lparen), recvCall.Fun, f, curPkg, "Worker", "w", nil)
	if !ok || tg.tid != "symbol:go:example.com/flow:(Worker).Error" {
		t.Errorf("w.Error() 应命中共用方法身份，got %+v ok=%v", tg, ok)
	}

	// 4) 局部变量遮蔽导入别名 → 方法身份，不得当包函数。
	shadowCall := findSelectorCall(t, calls["ShadowEntry"], "Error")
	tg, ok = tgts.lookupCall(fset.Position(shadowCall.Lparen), shadowCall.Fun, f, curPkg, "", "", nil)
	if !ok || tg.tid != "symbol:go:example.com/flow:(Worker).Error" {
		t.Errorf("遮蔽导入的局部 logger.Error() 不应借用包函数，got %+v ok=%v", tg, ok)
	}

	// 5) 嵌套 receiver（box.worker.Error()）→ 方法身份。
	nestedCall := findSelectorCall(t, calls["NestedEntry"], "Error")
	tg, ok = tgts.lookupCall(fset.Position(nestedCall.Lparen), nestedCall.Fun, f, curPkg, "", "", nil)
	if !ok || tg.tid != "symbol:go:example.com/flow:(Worker).Error" {
		t.Errorf("嵌套 receiver 调用不应借用包函数，got %+v ok=%v", tg, ok)
	}
}

// TestCallIdentityMethodOnlyCandidatesStayAmbiguous：只有方法候选时，未知
// receiver 不猜具体类型；多个同名方法保留歧义（不选第一个）。
func TestCallIdentityMethodOnlyCandidatesStayAmbiguous(t *testing.T) {
	fset, f, calls := parseCallIdentityFixture(t)
	shadowCall := findSelectorCall(t, calls["ShadowEntry"], "Error")
	single := &seqTargets{byName: map[string][]seqTarget{
		"Error": {{tid: "symbol:go:example.com/flow:(Worker).Error"}},
	}}
	if tg, ok := single.lookupCall(fset.Position(shadowCall.Lparen), shadowCall.Fun, f,
		"example.com/flow", "", "", nil); !ok || tg.tid != "symbol:go:example.com/flow:(Worker).Error" {
		t.Errorf("唯一方法候选应可恢复身份，got %+v ok=%v", tg, ok)
	}
	// 两个同名方法但 receiver 类型可证明（Worker）→ 按类型消歧。
	two := &seqTargets{byName: map[string][]seqTarget{
		"Error": {
			{tid: "symbol:go:example.com/flow:(Worker).Error"},
			{tid: "symbol:go:example.com/flow:(Other).Error"},
		},
	}}
	if tg, ok := two.lookupCall(fset.Position(shadowCall.Lparen), shadowCall.Fun, f,
		"example.com/flow", "", "", nil); !ok || tg.tid != "symbol:go:example.com/flow:(Worker).Error" {
		t.Errorf("可证明 receiver 类型时应消歧到 (Worker).Error，got %+v ok=%v", tg, ok)
	}
	// 两个同名方法 + receiver 类型不可证明（err.Error()）→ 保持歧义（不选第一个）。
	var errCall *ast.CallExpr
	for _, c := range calls["Entry"] {
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" &&
			sel.X.(*ast.Ident).Name == "err" {
			errCall = c
		}
	}
	if tg, ok := two.lookupCall(fset.Position(errCall.Lparen), errCall.Fun, f,
		"example.com/flow", "", "", nil); ok {
		t.Errorf("未知 receiver 的多方法候选应保持歧义（不选第一个），got %+v", tg)
	}
}

// TestCallIdentityPackageOnlyCandidateNotBorrowed：只有包函数候选时，实例
// selector 不得选中（旧行为会按名称借用）。
func TestCallIdentityPackageOnlyCandidateNotBorrowed(t *testing.T) {
	fset, f, calls := parseCallIdentityFixture(t)
	onlyPkg := &seqTargets{byName: map[string][]seqTarget{
		"Error": {{tid: "symbol:go:example.com/flow/log:Error"}},
	}}
	for _, fn := range []string{"MethodEntry", "ShadowEntry", "NestedEntry"} {
		c := findSelectorCall(t, calls[fn], "Error")
		if tg, ok := onlyPkg.lookupCall(fset.Position(c.Lparen), c.Fun, f,
			"example.com/flow", "", "", nil); ok {
			t.Errorf("%s: 实例 selector 不得借用包函数 %s", fn, tg.tid)
		}
	}
}
