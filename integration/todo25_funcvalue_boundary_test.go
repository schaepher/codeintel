//go:build integration

package integration

// todo25 §2.2–§2.4：多层装饰/工厂副作用/函数列表 order/空列表转发。

import (
	"testing"
)

const todo25DecorateFlow = `package continuation

func Entry() {
	f := func() { Sink() }
	f = Wrap(f)
	Use(f)
}
func Wrap(g func()) func() { Side(); return g }
func Use(g func())         { g() }
func Sink()                {}
func Side()                {}
func Never()               {}
`

const todo25ListFlow = `package continuation

func Entry() {
	list := []func(){A, B}
	list = append(list, C)
	for _, f := range list { f() }
}
func A() {}
func B() {}
func C() {}
`

const todo25EmptyListFlow = `package continuation

func Entry() {
	var l []func()
	Forward(l)
	for range l { Never() }
}
func Forward(l []func()) {
	for _, f := range l { f() }
}
func Never() {}
`

func TestTodo25FuncValueBoundaries(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	cases := []struct {
		name string
		flow string
		one  []string
		zero []string
	}{
		{"decorate", todo25DecorateFlow, []string{":Sink", ":Side"},
			[]string{":Never"}},
		{"list", todo25ListFlow, []string{":A", ":B", ":C"}, nil},
		{"empty-list", todo25EmptyListFlow, nil, []string{":Never"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := todo25ContinuationRepo(t, tc.flow)
			root := seqTreeJSON(t, dir, "symbol:go:example.com/continuation:Entry", 12)
			for _, sym := range tc.one {
				if countSeqSym(root, sym) == 0 {
					t.Errorf("%s 应被执行: %s", tc.name, sym)
				}
			}
			for _, sym := range tc.zero {
				if countSeqSym(root, sym) != 0 {
					t.Errorf("%s 不应执行: %s (%d)", tc.name, sym, countSeqSym(root, sym))
				}
			}
		})
	}
}
