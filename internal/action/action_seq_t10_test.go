package action

// T10：外部依赖回调边界——不继承调用者同名参数、失败定位请求内缓存、
// 取消中止定位（go list 用 ctx）。

import (
	"context"
	"strings"
	"testing"
)

// 调用者有自己的同名形参 `f`（未绑定）；外部函数用**自己的**形参 cb 绑定
// 调用点实参——回调必须展开（不能借用/污染调用者的 f）。
func TestT10ExternalRebindsOwnParams(t *testing.T) {
	appSrc := `package app

import "example.com/ext"

func Inner() {}

func Entry(f func()) {
	ext.RunBound(func() { Inner() })
}
`
	acts, app := seedSeqFuncValueRepoInDir(t, appSrc)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: app, Depth: 5})
	if err != nil {
		t.Fatal(err)
	}
	runs := seqCallNodesNamed(root, "ext.RunBound")
	if len(runs) != 1 {
		t.Fatalf("应有 1 个 ext.RunBound，got %d", len(runs))
	}
	if runs[0].Evidence == nil || !strings.HasPrefix(runs[0].Evidence.SymbolID, "symbol:go:example.com/ext:") {
		t.Fatalf("外部目标身份应准确: %+v", runs[0].Evidence)
	}
	callbacks := seqCallNodesNamed(runs[0], "cb")
	if len(callbacks) != 1 {
		t.Fatalf("外部形参 cb() 应绑定调用点实参并展开，got %d", len(callbacks))
	}
	if got := len(seqCallNodesNamed(callbacks[0], "Inner")); got != 1 {
		t.Errorf("回调内部调用应展开一次，got %d", got)
	}
}

// 已取消的 ctx：外部定位直接返回取消错误（不跑完 go list/解析）。
func TestT10ExternalResolveCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := seqGoListPkg(ctx, t.TempDir(), "example.com/ext"); err == nil {
		t.Fatal("已取消的 ctx 应中止外部定位")
	}
	if files := parseSeqDir(ctx, t.TempDir(), "<external>/x"); files != nil {
		t.Fatal("已取消的 ctx 不应解析任何外部源码文件")
	}
}

// 请求内缓存：两个调用点指向同一外部函数 → 只定位一次；失败定位同样缓存。
func TestT10ExternalLoadCachedPerRequest(t *testing.T) {
	appSrc := `package app

import "example.com/ext"

func Inner() {}

func Entry() {
	ext.RunBound(func() { Inner() })
	ext.RunBound(func() { Inner() })
}
`
	acts, app := seedSeqFuncValueRepoInDir(t, appSrc)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: app, Depth: 5})
	if err != nil {
		t.Fatal(err)
	}
	if root.Meta == nil || root.Meta.Timing == nil {
		t.Fatal("缺 timing")
	}
	if got := root.Meta.Timing.ExternalLoads; got != 1 {
		t.Errorf("同一外部包在请求内应只定位一次，got %d", got)
	}
	if got := len(seqCallNodesNamed(root, "Inner")); got != 2 {
		t.Errorf("两个调用点各执行一次回调，got %d", got)
	}

	missSrc := `package app

import "example.com/missing"

func Entry() { missing.Do(); missing.Do() }
`
	acts2, app2 := seedSeqFuncValueRepoInDir(t, missSrc)
	root2, err := acts2.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: app2, Depth: 3})
	if err != nil {
		t.Fatalf("外部包缺失不应让查询失败: %v", err)
	}
	if root2.Meta == nil || root2.Meta.Timing == nil {
		t.Fatal("缺 timing")
	}
	if got := root2.Meta.Timing.ExternalLoads; got != 1 {
		t.Errorf("失败定位也应请求内缓存（只尝试一次），got %d", got)
	}
}
