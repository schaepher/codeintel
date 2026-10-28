//go:build integration

package integration

// todo24 §11.2：同包兄弟文件的裸函数名工厂返回类型（真实 init → CLI）。
//
// flow.go 的 `Bridge()` 在本文件没有声明（声明在 factory.go），`Build()` 位于
// 兄弟文件且返回 *Impl；期望 depth=1 就选中 (Impl).Run（字段数据流，不是接口
// 候选），depth=8 继续到 Sink。反例把 Bridge 改成通过局部函数值调用（遮蔽），
// 不得再猜 Impl。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const siblingFactoryFlow = `package flow

type API interface{ Run() }

type Service struct{ Biz API }

func NewService() *Service { return &Service{Biz: Bridge()} }

func Bridge() API { return Build() }

func (s *Service) Run() { s.Biz.Run() }
`

const siblingFactoryFactory = `package flow

type Impl struct{}

func Build() *Impl { return &Impl{} }

func (*Impl) Run() { Sink() }

func Sink() {}
`

const siblingFactoryShadowFlow = `package flow

type API interface{ Run() }

type Service struct{ Biz API }

var runtimeFactory func() API

func NewService() *Service { return &Service{Biz: Bridge()} }

func Bridge() API { Build := runtimeFactory; return Build() }

func (s *Service) Run() { s.Biz.Run() }
`

func setupSiblingFactoryRepo(t *testing.T, flowSrc string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), flowSrc)
	writeFile(t, filepath.Join(dir, "factory.go"), siblingFactoryFactory)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// runTarget 取 (Service).Run 的 s.Biz.Run 调用节点。
func runTarget(t *testing.T, root *action.CodeSeqNode) *action.CodeSeqNode {
	t.Helper()
	var found *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Kind == "call" && strings.Contains(n.Label, "Biz.Run") {
			found = n
		}
	})
	return found
}

func TestSiblingFactoryReturnRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := setupSiblingFactoryRepo(t, siblingFactoryFlow)
	sym := "symbol:go:example.com/flow:(Service).Run"

	shallow := seqTreeJSON(t, dir, sym, 1, "--evidence")
	call := runTarget(t, shallow)
	if call == nil {
		t.Fatalf("depth=1 找不到 s.Biz.Run 调用: %+v", shallow)
	}
	if call.Evidence == nil || call.Evidence.SymbolID != "symbol:go:example.com/flow:(Impl).Run" {
		t.Fatalf("depth=1 应准确选定 (Impl).Run，got %+v", call.Evidence)
	}
	if call.Evidence.Ambiguous || len(call.Evidence.Candidates) > 1 {
		t.Errorf("兄弟文件工厂来源应是字段数据流而非候选：%+v", call.Evidence)
	}
	if call.Evidence.Reason == "" || strings.Contains(call.Evidence.Reason, "iface") {
		t.Errorf("证据来源不应是接口候选，got %q", call.Evidence.Reason)
	}

	deep := seqTreeJSON(t, dir, sym, 8)
	if !hasSymbolWith(deep, "(Impl).Run") {
		t.Errorf("depth=8 应展开到 (Impl).Run: %+v", deep)
	}
	if !hasSymbolWith(deep, ":Sink") {
		t.Errorf("depth=8 应继续到 Sink: %+v", deep)
	}
}

func TestSiblingFactoryShadowedNotGuessed(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := setupSiblingFactoryRepo(t, siblingFactoryShadowFlow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/flow:(Service).Run", 8)
	call := runTarget(t, root)
	if call != nil && call.Evidence != nil &&
		call.Evidence.SymbolID == "symbol:go:example.com/flow:(Impl).Run" {
		t.Errorf("局部函数值遮蔽时不得回退同名包函数：%+v", call.Evidence)
	}
	if hasSymbolWith(root, ":Sink") {
		t.Errorf("未知工厂不得展开到 Sink: %+v", root)
	}
}
