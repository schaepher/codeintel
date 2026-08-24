package ast

// R37 编译期接口断言 → implements 边测试：`var _ Iface = new(T)` /
// `var _ Iface = &T{}` / `var _ Iface = T{}` → 接口 → 实现者边；右侧是
// 接口/非实现类型 → 不建边。测试先行。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestInterfaceAssertionImplements：断言 `var _ Iface = new(impl)` 建立
// implements 边（接口 → 实现者）——SCIP 盲区补丁（go2o 实测场景）。
func TestInterfaceAssertionImplements(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"svc/api.go": `package svc

type Greeter interface {
	Say(name string) error
}

type greeterImpl struct{}

func (g *greeterImpl) Say(name string) error { return nil }

// 编译期断言（new 形态）
var _ Greeter = new(greeterImpl)
`,
	})
	edge := false
	for _, f := range facts {
		if f.Kind == domain.FactImplements &&
			string(f.SourceID) == "symbol:go:example.com/mtest/svc:Greeter" &&
			string(f.TargetID) == "symbol:go:example.com/mtest/svc:greeterImpl" {
			edge = true
		}
	}
	if !edge {
		t.Error("断言 new(impl) 未建立 implements 边（接口 → 实现者）")
	}
	_ = nodes
}

// TestInterfaceAssertionForms：断言形态矩阵——&T{} / T{} 值字面量 /
// 右侧是接口（无实现者）/ 非实现类型（未满足接口）→ 边界正确。
func TestInterfaceAssertionForms(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"svc/api.go": `package svc

type Greeter interface {
	Say(name string) error
}

type greeterImpl struct{}

func (g *greeterImpl) Say(name string) error { return nil }

type fakeImpl struct{}

// &T{} 形态
var _ Greeter = &greeterImpl{}

// T{} 值形态
var _ Greeter = greeterImpl{}

// 右侧是接口（noImpl 无实现者信息——不得建边）
type noImpl interface{ Say(name string) error }
var _ Greeter = noImpl(nil)

// 未满足接口的类型（fakeImpl 无 Say——不得建边）
var _ Greeter = fakeImpl{}
`,
	})
	count := 0
	for _, f := range facts {
		if f.Kind == domain.FactImplements && string(f.SourceID) == "symbol:go:example.com/mtest/svc:Greeter" {
			count++
		}
	}
	// &T{} + T{} 两条边；接口右侧与 fakeImpl 不应建边
	if count != 2 {
		t.Errorf("implements 边数 = %d; want 2（&T{} + T{}；接口/未实现不应建边）", count)
	}
	_ = nodes
}
