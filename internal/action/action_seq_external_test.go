package action

// todo22 §4.2 测试：外部依赖按需源码解析（go list 离线只读 + 请求内缓存）、
// 形参重新绑定（只有外部实现真的调用形参，回调才算已证明执行）、
// depth=1 只留准确外部目标不启动 go list、不修改 go.mod/go.sum。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const extRunSrc = `package ext

import "context"

func Run(ctx context.Context, f func(context.Context) error) error {
	return f(ctx)
}

func Store(f func()) {
	_ = f
}
`

// TestSeqExternalCallbackOnlyWhenCalled：外部函数真的调用形参 → 回调执行；
// 只保存不调用 → 回调不计入已执行。
func TestSeqExternalCallbackOnlyWhenCalled(t *testing.T) {
	appSrc := `package app

import (
	"context"

	"example.com/ext"
)

func Inner() {}

func Entry(ctx context.Context) {
	ext.Run(ctx, func(inner context.Context) error {
		Inner()
		return nil
	})
	ext.Store(func() { Inner() })
}
`
	acts, app := seedSeqFuncValueRepoInDir(t, appSrc)

	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: app, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	runs := seqCallNodesNamed(root, "ext.Run")
	if len(runs) != 1 {
		t.Fatalf("应有 1 个 ext.Run 节点，got %d", len(runs))
	}
	ev := runs[0].Evidence
	if ev == nil || ev.Source != SeqEvidenceExternal {
		t.Fatalf("外部函数应按需解析源码（source=external_source），got %+v", ev)
	}
	if ev.SymbolID != "symbol:go:example.com/ext:Run" {
		t.Errorf("外部目标 ID 应准确，got %q", ev.SymbolID)
	}
	// 外部函数体 f(ctx) 是形参调用 → 回调按绑定展开 → Inner 一次
	callbacks := seqCallNodesNamed(runs[0], "f")
	if len(callbacks) != 1 {
		t.Fatalf("外部函数体里的 f(ctx) 应展开为回调执行，got %d", len(callbacks))
	}
	if got := seqCallNodesNamed(callbacks[0], "Inner"); len(got) != 1 {
		t.Errorf("回调内部调用应展开（Inner 一次），got %d", len(got))
	}
	// ext.Store 只保存形参不调用 → 回调不得计入已执行
	stores := seqCallNodesNamed(root, "ext.Store")
	if len(stores) != 1 {
		t.Fatalf("应有 1 个 ext.Store 节点，got %d", len(stores))
	}
	if got := seqCallNodesNamed(stores[0], "Inner"); len(got) != 0 {
		t.Errorf("外部实现未调用形参时不得执行回调（Inner 出现 %d 次）", len(got))
	}
}

// TestSeqExternalDepthOneKeepsTarget：depth=1 保留准确外部目标 + depth 诊断，
// 不展开（也不启动 go list——用“没有子节点”间接证明）。
func TestSeqExternalDepthOneKeepsTarget(t *testing.T) {
	appSrc := `package app

import (
	"context"

	"example.com/ext"
)

func Inner() {}

func Entry(ctx context.Context) {
	ext.Run(ctx, func(inner context.Context) error { Inner(); return nil })
}
`
	acts, app := seedSeqFuncValueRepoInDir(t, appSrc)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: app, Depth: 1})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	runs := seqCallNodesNamed(root, "ext.Run")
	if len(runs) != 1 {
		t.Fatalf("应有 1 个 ext.Run，got %d", len(runs))
	}
	n := runs[0]
	if n.Evidence == nil || n.Evidence.SymbolID != "symbol:go:example.com/ext:Run" {
		t.Errorf("depth=1 也要保留准确外部目标，got %+v", n.Evidence)
	}
	if n.Stop == nil || n.Stop.Code != SeqStopDepthLimit {
		t.Errorf("depth=1 应给 depth 诊断，got %+v", n.Stop)
	}
	if len(n.Nodes) != 0 {
		t.Errorf("depth=1 不得展开外部源码，got %d 子节点", len(n.Nodes))
	}
	if got := seqCallNodesNamed(root, "Inner"); len(got) != 0 {
		t.Errorf("depth=1 不得执行回调（Inner 出现 %d 次）", len(got))
	}
}

// TestSeqExternalReadOnlyAndMissing：go list 不修改 go.mod/go.sum；找不到外部
// 包时保持 external_target + 可读原因（不崩、不猜）。
func TestSeqExternalReadOnlyAndMissing(t *testing.T) {
	appSrc := `package app

import "example.com/missing"

func Entry() { missing.Do() }
`
	acts, app := seedSeqFuncValueRepoInDir(t, appSrc)
	gomodBefore, err := os.ReadFile(filepath.Join(app, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Entry", RepoAbs: app, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence 不应因外部包缺失而失败: %v", err)
	}
	nodes := seqCallNodesNamed(root, "missing.Do")
	if len(nodes) != 1 {
		t.Fatalf("应有 1 个 missing.Do 节点，got %d", len(nodes))
	}
	if nodes[0].Stop == nil || nodes[0].Stop.Code != SeqStopExternalTarget {
		t.Errorf("外部包不可定位应给 external_target，got %+v", nodes[0].Stop)
	}
	if nodes[0].Stop != nil && !strings.Contains(nodes[0].Stop.Detail, "example.com/missing") {
		t.Errorf("失败原因应包含 import path，got %q", nodes[0].Stop.Detail)
	}
	gomodAfter, err := os.ReadFile(filepath.Join(app, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gomodBefore) != string(gomodAfter) {
		t.Errorf("查询阶段不得修改 go.mod\nbefore:\n%s\nafter:\n%s", gomodBefore, gomodAfter)
	}
	if _, err := os.Stat(filepath.Join(app, "go.sum")); err == nil {
		t.Errorf("查询阶段不得生成 go.sum")
	}
}
