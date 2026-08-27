package ast

import "go/ast"
import "go/parser"
import "go/token"
import "testing"
import "github.com/schaepher/codeintel/internal/domain"

// TestChainedPosAlign（用户提示）：a().b() 链式调用的 pos 对齐——
// 发射端 call.Pos()（Lparen 位置）与消费端 fset.Position(call.Pos())
// 必须一致；同行链式 + 并列调用各自独立。
func TestChainedPosAlign(t *testing.T) {
	src := `package mtest

import "example.com/mtest/aa"

func run() {
	aa.NewService().DoSth(); aa.Helper()
}
`
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"aa/aa.go": `package aa

type Service interface {
	DoSth()
}

type svcImpl struct{}

func (s *svcImpl) DoSth() {}

// 构造器：声明返回接口、函数体 return 具体实现
func NewService() Service {
	return &svcImpl{}
}

func Helper() {}
`,
		"main.go": src,
	})

	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "main.go", src, 0)
	doSthOff, helperOff := -1, -1
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 {
				switch sel.Sel.Name {
				case "DoSth":
					doSthOff = fset.Position(call.Lparen).Offset
				case "Helper":
					helperOff = fset.Position(call.Lparen).Offset
				}
			}
		}
		return true
	})
	got := map[string]float64{}
	for _, f := range facts {
		if f.Kind == domain.FactCalls && string(f.SourceID) == "symbol:go:example.com/mtest:run" {

			switch v := f.Metadata["pos"].(type) {
			case float64:
				got[string(f.TargetID)] = v
			case int:
				got[string(f.TargetID)] = float64(v)
			}
		}
	}

	if p := got["symbol:go:example.com/mtest/aa:(svcImpl).DoSth"]; int(p) != doSthOff {
		t.Errorf("DoSth 边 pos = %d; want %d（call.Pos()=Lparen——与消费端一致）", int(p), doSthOff)
	}
	if p := got["symbol:go:example.com/mtest/aa:Helper"]; int(p) != helperOff {
		t.Errorf("Helper 边 pos = %d; want %d", int(p), helperOff)
	}

	if int(got["symbol:go:example.com/mtest/aa:(svcImpl).DoSth"]) == int(got["symbol:go:example.com/mtest/aa:Helper"]) {
		t.Error("同行链式 + 并列调用 pos 应不同（各自独立）")
	}
}

// TestChainedVarMethodPos（用户实测）：同行 x.a().b()——x 是对象变量，
// 内层 x.a() 与外层 x.a().b() 两条 calls 边的 pos 应不同（各自 Lparen）。
func TestChainedVarMethodPos(t *testing.T) {
	src := `package mtest

type X struct{}

func (x *X) a() *X { return x }

func (x *X) b() {}

func run() {
	x := &X{}
	x.a().b()
}
`
	_, facts := indexFixture(t, map[string]string{
		"go.mod":  "module example.com/mtest\n\ngo 1.21\n",
		"main.go": src,
	})

	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "main.go", src, 0)
	aOff, bOff := -1, -1
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 {
				switch sel.Sel.Name {
				case "a":
					aOff = fset.Position(call.Lparen).Offset
				case "b":
					bOff = fset.Position(call.Lparen).Offset
				}
			}
		}
		return true
	})
	if aOff < 0 || bOff < 0 || aOff == bOff {
		t.Fatalf("parser 预期 offset 异常 a=%d b=%d（应不同——各自 Lparen）", aOff, bOff)
	}
	got := map[string]int{}
	for _, fact := range facts {
		if fact.Kind == domain.FactCalls && string(fact.SourceID) == "symbol:go:example.com/mtest:run" {
			switch v := fact.Metadata["pos"].(type) {
			case float64:
				got[string(fact.TargetID)] = int(v)
			case int:
				got[string(fact.TargetID)] = v
			}
		}
	}

	if p, ok := got["symbol:go:example.com/mtest:(X).a"]; ok && p != aOff {
		t.Errorf("内层 a 边 pos = %d; want %d（a 的 Lparen——用户实测相同即此 bug）", p, aOff)
	}
	if p, ok := got["symbol:go:example.com/mtest:(X).b"]; !ok {
		t.Errorf("外层 b 边缺失")
	} else if p != bOff {
		t.Errorf("外层 b 边 pos = %d; want %d（b 的 Lparen）", p, bOff)
	}
	if pa, ok1 := got["symbol:go:example.com/mtest:(X).a"]; ok1 {
		if pb, ok2 := got["symbol:go:example.com/mtest:(X).b"]; ok2 && pa == pb {
			t.Errorf("a 与 b 的 pos 相同 = %d（用户实测 bug——同行链式互不区分）", pa)
		}
	}
}
