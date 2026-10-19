package ast

import (
	"go/token"
	"go/types"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// testFuncPkg 构造仅用于节点渲染的 packages.Package + *types.Func（签名
// 引用本包具名类型 T，便于观察 RelativeTo 限定符差异）。
func testFuncPkg(pkgPath, pkgName, fnName string) (*packages.Package, *types.Func) {
	tp := types.NewPackage(pkgPath, pkgName)
	tn := types.NewTypeName(token.NoPos, tp, "T", nil)
	named := types.NewNamed(tn, types.NewStruct(nil, nil), nil)
	tp.Scope().Insert(tn)
	param := types.NewVar(token.NoPos, tp, "x", named)
	sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(param), types.NewTuple(), false)
	fn := types.NewFunc(token.NoPos, tp, fnName, sig)
	return &packages.Package{Fset: token.NewFileSet(), Types: tp}, fn
}

// TestNodeForCache（todo10-1）：同函数/限定符/标记 → 同指针；标记与
// 限定符不同 → 不同变体；未知标记不缓存；reset 后重建。
func TestNodeForCache(t *testing.T) {
	repo := &domain.Repository{Path: t.TempDir()}
	a := &Adapter{modules: []string{"example.com/mtest"}}
	pkg, fn := testFuncPkg("example.com/mtest", "mtest", "F")
	id := domain.CanonicalID("symbol:go:example.com/mtest:F")

	n1 := a.nodeFor(repo, pkg, fn, id, domain.KindFunction, nil)
	n2 := a.nodeFor(repo, pkg, fn, id, domain.KindFunction, nil)
	if n1 != n2 {
		t.Fatal("同键应命中缓存")
	}
	if n1.Properties["signature"] != "func F(x T)" {
		t.Fatalf("signature = %v", n1.Properties["signature"])
	}

	httpNode := a.nodeFor(repo, pkg, fn, id, domain.KindFunction, map[string]bool{"serves_http": true})
	if httpNode == n1 || httpNode.Properties["serves_http"] != "true" {
		t.Fatalf("serves_http 变体不正确: %+v", httpNode.Properties)
	}
	if again := a.nodeFor(repo, pkg, fn, id, domain.KindFunction, map[string]bool{"serves_http": true}); again != httpNode {
		t.Fatal("serves_http 变体应命中缓存")
	}
	grpcNode := a.nodeFor(repo, pkg, fn, id, domain.KindFunction, map[string]bool{"serves_grpc": true})
	if grpcNode == n1 || grpcNode == httpNode || grpcNode.Properties["serves_grpc"] != "true" {
		t.Fatal("serves_grpc 变体不正确")
	}

	// 未知标记：行为不变但不缓存（避免键覆盖不到时返回错误变体）
	u1 := a.nodeFor(repo, pkg, fn, id, domain.KindFunction, map[string]bool{"unknown_flag": true})
	u2 := a.nodeFor(repo, pkg, fn, id, domain.KindFunction, map[string]bool{"unknown_flag": true})
	if u1 == u2 {
		t.Fatal("未知标记不应缓存")
	}
	if u1.Properties["unknown_flag"] != "true" {
		t.Fatal("未知标记仍应写入属性")
	}

	// 不同限定符包 → signature 渲染不同，不能共享节点
	pkg2, _ := testFuncPkg("example.com/other", "other", "G")
	n3 := a.nodeFor(repo, pkg2, fn, id, domain.KindFunction, nil)
	if n3 == n1 {
		t.Fatal("不同限定符包不应共享节点")
	}
	if n3.Properties["signature"] == n1.Properties["signature"] {
		t.Fatalf("限定符不同但 signature 相同: %v", n3.Properties["signature"])
	}

	// 外部函数不缓存（避免按调用方包产生大量长期驻留变体）
	_, extFn := testFuncPkg("example.com/ext", "ext", "E")
	e1 := a.nodeFor(repo, pkg, extFn, "symbol:go:example.com/ext:E", domain.KindFunction, nil)
	e2 := a.nodeFor(repo, pkg, extFn, "symbol:go:example.com/ext:E", domain.KindFunction, nil)
	if e1 == e2 {
		t.Fatal("外部函数不应缓存")
	}

	a.resetNodeCache()
	if n4 := a.nodeFor(repo, pkg, fn, id, domain.KindFunction, nil); n4 == n1 {
		t.Fatal("reset 后应重建节点")
	}
}

// TestMethodSetContains（todo10-2）：集合包含判定语义。
func TestMethodSetContains(t *testing.T) {
	set := map[string]bool{"A": true, "B": true}
	if !methodSetContains(set, []string{"A", "B"}) || !methodSetContains(set, nil) {
		t.Fatal("包含判定应为 true")
	}
	if methodSetContains(set, []string{"A", "C"}) {
		t.Fatal("缺方法应判 false")
	}
}
