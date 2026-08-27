package ast

// W1 测试（用户需求）：调用解析不能只记录接口类型，必须包含接口
// 方法定义——接口方法调用（参数/变量形态，静态无法确定实现）的
// calls 边 target 应为接口方法节点 (Iface).Method（含方法名）——
// 时序图据此具体化到实现并展开。修复前 target 是接口类型节点
// （无方法名），时序图无法展开。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestIfaceCallTargetHasMethodName：参数形态（m Manager 参数，无静态
// 实现）→ calls 边 target 指向接口方法 (Manager).Handle，接口方法
// 节点发射。
func TestIfaceCallTargetHasMethodName(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"main.go": `package mtest

type Manager interface {
	Handle()
}

func use(m Manager) {
	m.Handle()
}
`,
	})
	// 接口方法节点存在（含方法名）
	foundNode := false
	for _, n := range nodes {
		if n.ID == "symbol:go:example.com/mtest:(Manager).Handle" && n.Kind == domain.KindMethod {
			foundNode = true
		}
	}
	if !foundNode {
		t.Fatal("接口方法节点 (Manager).Handle 应发射（时序图具体化依据）")
	}
	// calls 边 target = 接口方法（含方法名），而非接口类型
	found := false
	for _, f := range facts {
		if f.Kind == domain.FactCalls && string(f.TargetID) == "symbol:go:example.com/mtest:(Manager).Handle" {
			found = true
		}
	}
	if !found {
		t.Error("接口调用边 target 应为 (Manager).Handle（含方法名——修复前是接口类型 Manager 无方法名）")
	}
}

// TestIfaceCallConcreteStillImpl：链式调用能确定实现（构造器 return
// 具体类型）→ 仍指向具体实现方法（W1 不破坏 R18 具体化）。
func TestIfaceCallConcreteStillImpl(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"main.go": `package mtest

type Manager interface {
	Handle()
}

type engImpl struct{}

func (e *engImpl) Handle() {}

// 构造器：声明返回接口、函数体 return 具体实现
func newEng() Manager {
	return &engImpl{}
}

func run() {
	newEng().Handle()
}
`,
	})
	found := false
	for _, f := range facts {
		if f.Kind == domain.FactCalls && string(f.TargetID) == "symbol:go:example.com/mtest:(engImpl).Handle" {
			found = true
		}
	}
	if !found {
		t.Error("链式调用能确定实现时应指向 (engImpl).Handle（具体化优先于接口方法回退）")
	}
}
