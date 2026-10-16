package ssa

import (
	"go/token"
	"go/types"
	"sync"
	"testing"
)

// sampleTypes 覆盖基础/指针/切片/map/签名/结构体/接口/泛型实例化。
func sampleTypes(t *testing.T) []types.Type {
	t.Helper()
	pkg := types.NewPackage("example.com/m", "m")
	sig := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, pkg, "x", types.Typ[types.Int])),
		types.NewTuple(types.NewVar(token.NoPos, pkg, "", types.Typ[types.String])), false)
	st := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, pkg, "A", types.Typ[types.Int], false),
	}, nil)
	iface := types.NewInterfaceType([]*types.Func{
		types.NewFunc(token.NoPos, pkg, "M", types.NewSignatureType(nil, nil, nil, nil, nil, false)),
	}, nil).Complete()

	// 泛型：Box[T] 实例化为 Box[int]。
	tpName := types.NewTypeName(token.NoPos, pkg, "T", nil)
	tparam := types.NewTypeParam(tpName, types.NewInterfaceType(nil, nil).Complete())
	box := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Box", nil), types.NewStruct(nil, nil), nil)
	box.SetTypeParams([]*types.TypeParam{tparam})
	inst, err := types.Instantiate(nil, box, []types.Type{types.Typ[types.Int]}, false)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	return []types.Type{
		types.Typ[types.Int],
		types.NewPointer(types.Typ[types.String]),
		types.NewSlice(types.Typ[types.Int]),
		types.NewMap(types.Typ[types.String], types.Typ[types.Int]),
		sig,
		st,
		iface,
		types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Named", nil), st, nil),
		inst,
	}
}

// TestStringCacheMatchesDirect：缓存结果必须与直接 String() 逐字一致
// （含泛型实例化），第二次访问命中同一结果。
func TestStringCacheMatchesDirect(t *testing.T) {
	s := &funcSnapshot{}
	for _, tt := range sampleTypes(t) {
		if tt == nil {
			continue
		}
		want := tt.String()
		if got := s.typeString(tt); got != want {
			t.Errorf("typeString(%v) = %q, want %q", tt, got, want)
		}
		if got := s.typeString(tt); got != want {
			t.Errorf("typeString 命中结果 = %q, want %q", got, want)
		}
	}
	if got := s.typeString(nil); got != "" {
		t.Errorf("nil 类型应返回空串，got %q", got)
	}
}

// TestCandidateKeyCacheMatchesDirect：候选键缓存与纯函数一致，nil 返回空串。
func TestCandidateKeyCacheMatchesDirect(t *testing.T) {
	pkg := types.NewPackage("example.com/m", "m")
	recv := types.NewVar(token.NoPos, pkg, "r", types.NewPointer(types.Typ[types.Int]))
	fn := types.NewFunc(token.NoPos, pkg, "Method", types.NewSignatureType(recv, nil, nil, nil, nil, false))
	plain := types.NewFunc(token.NoPos, pkg, "Plain", types.NewSignatureType(nil, nil, nil, nil, nil, false))

	s := &funcSnapshot{}
	for _, f := range []*types.Func{fn, plain} {
		want := candidateKey(f)
		if got := s.candidateKeyOf(f); got != want {
			t.Errorf("candidateKeyOf(%s) = %q, want %q", f.Name(), got, want)
		}
	}
	if got := s.candidateKeyOf(nil); got != "" {
		t.Errorf("nil 函数应返回空串，got %q", got)
	}
}

// TestStringCacheConcurrentFirstAccess：并发首次访问（同/不同类型）结果一致，
// 配合 -race 验证加锁正确。
func TestStringCacheConcurrentFirstAccess(t *testing.T) {
	s := &funcSnapshot{}
	typesList := sampleTypes(t)
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan string, workers*len(typesList))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tt := typesList[(seed+i)%len(typesList)]
				if got, want := s.typeString(tt), tt.String(); got != want {
					errs <- got + "!=" + want
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("并发结果不一致: %s", e)
	}
}

// TestStringCacheBuildIsolation：缓存挂在 funcSnapshot 上，两个快照互不共享
// （不跨 SSA Program 复用）。
func TestStringCacheBuildIsolation(t *testing.T) {
	a := &funcSnapshot{}
	b := &funcSnapshot{}
	a.typeString(types.Typ[types.Int])
	if len(a.typeStrs) == 0 {
		t.Fatal("a 应已缓存类型字符串")
	}
	if len(b.typeStrs) != 0 {
		t.Errorf("b 不应看到 a 的缓存，got %d", len(b.typeStrs))
	}
}

// TestStringCacheZeroAllocOnHit：命中路径零分配。
func TestStringCacheZeroAllocOnHit(t *testing.T) {
	s := &funcSnapshot{}
	tt := sampleTypes(t)[2] // slice
	s.typeString(tt)
	if n := testing.AllocsPerRun(1000, func() { _ = s.typeString(tt) }); n != 0 {
		t.Errorf("typeString 命中分配 = %v, want 0", n)
	}

	pkg := types.NewPackage("example.com/m", "m")
	fn := types.NewFunc(token.NoPos, pkg, "M", types.NewSignatureType(
		types.NewVar(token.NoPos, pkg, "r", types.Typ[types.Int]), nil, nil, nil, nil, false))
	s.candidateKeyOf(fn)
	if n := testing.AllocsPerRun(1000, func() { _ = s.candidateKeyOf(fn) }); n != 0 {
		t.Errorf("candidateKeyOf 命中分配 = %v, want 0", n)
	}
}

// TestStringCacheExtractorFallback：提取器无 funcSnapshot（emitGlobalInit
// 轻量提取器）时回退直接渲染，不 panic。
func TestStringCacheExtractorFallback(t *testing.T) {
	ext := &fieldExtractor{}
	if got := ext.typeString(types.Typ[types.Int]); got != "int" {
		t.Errorf("fallback typeString = %q, want int", got)
	}
	if got := ext.typeString(nil); got != "" {
		t.Errorf("fallback nil = %q, want empty", got)
	}
}
