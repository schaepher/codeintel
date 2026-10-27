package action

// todo22 P1 测试：实现候选的到达定义按**声明对象**区分——同名变量/遮蔽
// 不串；"没有定义"与"定义为未知"分开（与函数值环境同一口径）。

import (
	"go/ast"
	"go/token"
	"testing"
)

const sceneImplObjSrc = `package m

type Doer interface{ Do() }

type A struct{}

func (A) Do() {}

type B struct{}

func (B) Do() {}

type C struct{}

func (C) Do() {}

func TwoVars() {
	a := &A{}
	b := &B{}
	a.Do()
	b.Do()
}

func Shadow() {
	d := &A{}
	{
		d := &B{}
		d.Do()
	}
	d.Do()
}

func Unknown() {
	var d Doer
	d = makeDoer()
	d.Do()
}

func Undeclared() {
	d := &C{}
	_ = d
}
`

// implCallAt 找函数体里指定标签的调用（按出现顺序），返回调用位置与接收者对象。
func implCallAt(t *testing.T, fset *token.FileSet, f *ast.File, fnName, label string, idx int) (*ast.BlockStmt, *ast.Ident, int) {
	t.Helper()
	var body *ast.BlockStmt
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Name.Name == fnName {
			body = fd.Body
		}
	}
	if body == nil {
		t.Fatalf("找不到函数 %s", fnName)
	}
	var hits []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if callLabel(fset, []byte(sceneImplObjSrc), call.Fun) == label {
			hits = append(hits, call)
		}
		return true
	})
	if idx >= len(hits) {
		t.Fatalf("%s 里 %s 调用数 %d，越界 %d", fnName, label, len(hits), idx)
	}
	call := hits[idx]
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		t.Fatalf("%s 不是方法调用", label)
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		t.Fatalf("%s 接收者不是标识符", label)
	}
	return body, id, int(call.Pos())
}

func implNames(defs []implDef) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.impl)
	}
	return out
}

// TestImplReachingPerObject：两个变量各自只拿到自己的实现定义。
func TestImplReachingPerObject(t *testing.T) {
	fset, f := parseSeqFile(t, sceneImplObjSrc)
	body, idA, posA := implCallAt(t, fset, f, "TwoVars", "a.Do", 0)
	aDefs, found := reachingImplsAt(fset, body, token.Pos(posA), idA.Obj, idA.Name)
	if !found || len(aDefs) != 1 || aDefs[0].impl != "A" {
		t.Errorf("a.Do 应只拿到 A 的定义，got %v (found=%v)", implNames(aDefs), found)
	}
	bodyB, idB, posB := implCallAt(t, fset, f, "TwoVars", "b.Do", 0)
	bDefs, found := reachingImplsAt(fset, bodyB, token.Pos(posB), idB.Obj, idB.Name)
	if !found || len(bDefs) != 1 || bDefs[0].impl != "B" {
		t.Errorf("b.Do 应只拿到 B 的定义，got %v (found=%v)", implNames(bDefs), found)
	}
}

// TestImplReachingShadowObject：内外层同名变量按对象身份区分（不串）。
func TestImplReachingShadowObject(t *testing.T) {
	fset, f := parseSeqFile(t, sceneImplObjSrc)
	body, innerID, innerPos := implCallAt(t, fset, f, "Shadow", "d.Do", 0)
	innerDefs, found := reachingImplsAt(fset, body, token.Pos(innerPos), innerID.Obj, innerID.Name)
	if !found || len(innerDefs) != 1 || innerDefs[0].impl != "B" {
		t.Errorf("内层 d.Do 应为 B，got %v", implNames(innerDefs))
	}
	_, outerID, outerPos := implCallAt(t, fset, f, "Shadow", "d.Do", 1)
	outerDefs, found := reachingImplsAt(fset, body, token.Pos(outerPos), outerID.Obj, outerID.Name)
	if !found || len(outerDefs) != 1 || outerDefs[0].impl != "A" {
		t.Errorf("外层 d.Do 应为 A，got %v", implNames(outerDefs))
	}
}

// TestImplReachingNoDefVsUnknown：没有定义（found=false）与定义为未知
// （found=true、defs 为空）必须区分。
func TestImplReachingNoDefVsUnknown(t *testing.T) {
	fset, f := parseSeqFile(t, sceneImplObjSrc)
	body, idU, posU := implCallAt(t, fset, f, "Unknown", "d.Do", 0)
	uDefs, found := reachingImplsAt(fset, body, token.Pos(posU), idU.Obj, idU.Name)
	if !found {
		t.Errorf("d 有定义（赋值为未知）→ found 应为 true")
	}
	if len(uDefs) != 0 {
		t.Errorf("定义为未知 → 不应有实现候选，got %v", implNames(uDefs))
	}
	// 同一函数里未声明的变量名
	if defs, found := reachingImplsAt(fset, body, token.Pos(posU), nil, "notDeclared"); found || len(defs) != 0 {
		t.Errorf("未声明变量应为 found=false，got %v", implNames(defs))
	}
}
