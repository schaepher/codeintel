//go:build integration

package integration

// todo25 §3.3：DI/AOP 剩余边界——闭包内注册、嵌入提升字段注解。
// 真实 init → CLI；只认确定执行/可证明的注册证据。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25DIClosureFlow = `package continuation

type API interface{ Insert() }
type Store struct{}
func (s *Store) Insert(){ InsertEvent() }
func InsertEvent(){}

type App struct {
	// @inject store
	Store API
}
func (a *App) Run(){ a.Store.Insert() }

type Container struct{}
func (c *Container) Register(key string, impl API){}
func Setup(){
	c := &Container{}
	reg := func(){ c.Register("store", &Store{}) }
	reg()
}
`

const todo25DIEmbeddedFlow = `package continuation

type API interface{ Insert() }
type Store struct{}
func (s *Store) Insert(){ InsertEvent() }
func InsertEvent(){}

type Base struct {
	// @inject store
	Store API
}
type App struct{ *Base }
func (a *App) Run(){ a.Store.Insert() }

type Container struct{}
func (c *Container) Register(key string, impl API){}
func Setup(){ c := &Container{}; c.Register("store", &Store{}) }
`

const todo25DIUnknownRewriteFlow = `package continuation

type API interface{ Insert() }
type Store struct{}
func (s *Store) Insert(){ InsertEvent() }
func InsertEvent(){}

type App struct {
	// @inject store
	Store API
}
func (a *App) Run(){ a.Store.Insert() }

type Container struct{}
func (c *Container) Register(key string, impl API){}
func (c *Container) Reset(key string){}
func Setup(){
	c := &Container{}
	c.Register("store", &Store{})
	c.Reset("store")
}
`

func TestTodo25DIClosureRegistration(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25DIClosureFlow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:(App).Run", 8)
	var hit *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Label == "a.Store.Insert" {
			hit = n
		}
	})
	if hit == nil {
		t.Fatalf("缺少 a.Store.Insert 节点: %+v", root)
	}
	if hit.Evidence == nil || hit.Evidence.Source != action.SeqEvidenceDI ||
		hit.Evidence.SymbolID != "symbol:go:example.com/continuation:(Store).Insert" {
		t.Fatalf("闭包内 Register 应作为 DI 证据: %+v", hit.Evidence)
	}
	if countSeqSym(root, ":InsertEvent") != 1 {
		t.Errorf("DI 具体实现应执行一次，got %d", countSeqSym(root, ":InsertEvent"))
	}
}

func TestTodo25DIEmbeddedFieldAnnotation(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25DIEmbeddedFlow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:(App).Run", 8)
	var hit *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Label == "a.Store.Insert" {
			hit = n
		}
	})
	if hit == nil {
		t.Fatalf("缺少 a.Store.Insert 节点: %+v", root)
	}
	if hit.Evidence == nil || hit.Evidence.Source != action.SeqEvidenceDI ||
		hit.Evidence.SymbolID != "symbol:go:example.com/continuation:(Store).Insert" {
		t.Fatalf("嵌入字段注解应作为 DI 证据: %+v", hit.Evidence)
	}
	if countSeqSym(root, ":InsertEvent") != 1 {
		t.Errorf("嵌入字段 DI 具体实现应执行一次，got %d", countSeqSym(root, ":InsertEvent"))
	}
}

// TestTodo25DIUnknownMethodRewrite：同一容器上的未知方法（Reset）可能替换/
// 移除注册——注册调用仍在也不得当成最终绑定。
func TestTodo25DIUnknownMethodRewrite(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25ContinuationRepo(t, todo25DIUnknownRewriteFlow)
	root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:(App).Run", 8)
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Label == "a.Store.Insert" && n.Evidence != nil &&
			n.Evidence.Source == action.SeqEvidenceDI &&
			n.Evidence.SymbolID == "symbol:go:example.com/continuation:(Store).Insert" {
			t.Errorf("未知容器方法改写后不得使用旧注册证据: %+v", n.Evidence)
		}
	})
	if countSeqSym(root, ":InsertEvent") != 0 {
		t.Errorf("未知容器方法改写后不应执行已注册实现，got %d", countSeqSym(root, ":InsertEvent"))
	}
}
