package ast

import (
	"go/token"
	"go/types"
	"sync"
	"testing"
)

// methodFunc 构造具名类型 + 值接收者方法（用于 fnID 缓存等价性测试）。
func methodFunc(pkg *types.Package, typeName, method string) *types.Func {
	tn := types.NewTypeName(token.NoPos, pkg, typeName, nil)
	named := types.NewNamed(tn, types.NewStruct(nil, nil), nil)
	recv := types.NewVar(token.NoPos, pkg, "r", named)
	sig := types.NewSignatureType(recv, nil, nil, types.NewTuple(), types.NewTuple(), false)
	fn := types.NewFunc(token.NoPos, pkg, method, sig)
	named.AddMethod(fn)
	return fn
}

// TestFnIDOfMatchesPure：缓存值与纯函数逐项一致（函数/方法/空/非签名类型）。
func TestFnIDOfMatchesPure(t *testing.T) {
	pkg := types.NewPackage("example.com/mtest", "mtest")
	plain := types.NewFunc(token.NoPos, pkg, "Plain", types.NewSignatureType(
		nil, nil, nil, types.NewTuple(), types.NewTuple(), false))
	noPkg := types.NewFunc(token.NoPos, nil, "Orphan", types.NewSignatureType(
		nil, nil, nil, types.NewTuple(), types.NewTuple(), false))
	cases := []*types.Func{plain, methodFunc(pkg, "T", "M"), noPkg, nil}

	a := &Adapter{}
	for _, fn := range cases {
		wantID, wantKind := fnID(fn)
		for i := 0; i < 3; i++ {
			gotID, gotKind := a.fnIDOf(fn)
			if gotID != wantID || gotKind != wantKind {
				t.Fatalf("fnIDOf(%v) = (%q,%q), want (%q,%q)", fn, gotID, gotKind, wantID, wantKind)
			}
		}
	}
	if len(cases) > 0 {
		if _, ok := a.fnIDs.Load(plain); !ok {
			t.Fatal("缓存未写入非 nil 函数")
		}
		if _, ok := a.fnIDs.Load((*types.Func)(nil)); ok {
			t.Fatal("nil 不应写入缓存")
		}
	}
}

// TestFnIDOfConcurrentSameResult：并发首次命中同一 func 结果一致（sync.Map）。
func TestFnIDOfConcurrentSameResult(t *testing.T) {
	pkg := types.NewPackage("example.com/mtest", "mtest")
	fn := methodFunc(pkg, "T", "M")
	a := &Adapter{}
	wantID, wantKind := fnID(fn)
	var wg sync.WaitGroup
	errs := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id, kind := a.fnIDOf(fn); id != wantID || kind != wantKind {
				errs <- "mismatch"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// TestResetFnIDCache：构建入口重置（不跨构建持有旧 Program 对象）。
func TestResetFnIDCache(t *testing.T) {
	pkg := types.NewPackage("example.com/mtest", "mtest")
	fn := types.NewFunc(token.NoPos, pkg, "F", types.NewSignatureType(
		nil, nil, nil, types.NewTuple(), types.NewTuple(), false))
	a := &Adapter{}
	a.fnIDOf(fn)
	if _, ok := a.fnIDs.Load(fn); !ok {
		t.Fatal("前置缓存未生效")
	}
	a.resetFnIDCache()
	if _, ok := a.fnIDs.Load(fn); ok {
		t.Fatal("resetFnIDCache 后仍残留")
	}
}
