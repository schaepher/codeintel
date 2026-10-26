package action

// P1-01：运行时配置 / 动态别名的来源定位（不改变判定等级）。
//
// 验收口径：
//   - 运行时来源（配置字段/环境变量/工厂实参/reflect/类型断言）→ 仍然
//     unverified，但原因必须**定位到表达式与行号**（不再是一句笼统的
//     "不是可解析的配置字面量"）
//   - 字面量配置的既有判定不变（同实例 → corroborated；不同字面量 →
//     mismatch）
//   - 非依赖运行时的 `&Bus{}` 空字面量仍按原规则（"{}" 视为无显式配置）

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// parsedFuncFromSrc 用源码片段构造 parsedFunc（同 parseFuncNode 的字段语义）。
func parsedFuncFromSrc(t *testing.T, src, fnName string) *parsedFunc {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "f.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fnName {
			continue
		}
		return &parsedFunc{
			id:     "symbol:go:example.com/m:" + fnName,
			pkg:    "example.com/m",
			file:   "f.go",
			src:    []byte(src),
			fset:   fset,
			decl:   fd,
			parsed: file,
		}
	}
	t.Fatalf("func %s not found", fnName)
	return nil
}

// callPosOf 函数体内指定方法调用的接收者 token.Pos（调用前绑定解析用）。
func callPosOf(t *testing.T, pf *parsedFunc, method string) token.Pos {
	t.Helper()
	var pos token.Pos
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || pos != token.NoPos {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != method {
			return true
		}
		pos = call.Pos()
		return true
	})
	if pos == token.NoPos {
		t.Fatalf("call %s not found", method)
	}
	return pos
}

const runtimeBindingSrc = `package m

import (
	"os"
	"reflect"
)

type Bus struct {
	Addr  string
	Topic string
}

var cfg struct{ Addr string }

func NewBus(addr string) *Bus { return &Bus{Addr: addr} }

func produceConfig() {
	b := &Bus{Addr: cfg.Addr}
	b.Send("topic")
}

func produceEnv() {
	b := &Bus{Addr: os.Getenv("BUS_ADDR")}
	b.Send("topic")
}

func produceReflect() {
	b := reflect.ValueOf(&Bus{}).Interface().(*Bus)
	b.Send("topic")
}

func produceAssert() {
	var v any = &Bus{}
	b := v.(*Bus)
	b.Send("topic")
}

func produceFactory() {
	b := NewBus(cfg.Addr)
	b.Send("topic")
}

func consumeLiteral() {
	c := &Bus{Addr: "literal-addr", Topic: "t"}
	c.Receive("topic")
}

func produceLiteral() {
	p := &Bus{Addr: "literal-addr", Topic: "t"}
	p.Send("topic")
}

func consumeOther() {
	q := &Bus{Addr: "other-addr"}
	q.Receive("topic")
}
`

func TestRuntimeBindingSourcesLocated(t *testing.T) {
	cases := []struct {
		fn    string
		wants []string
	}{
		{"produceConfig", []string{"运行时配置字段", "cfg.Addr", "L"}},
		{"produceEnv", []string{"环境变量", "os.Getenv(\"BUS_ADDR\")", "L"}},
		{"produceReflect", []string{"reflect", "动态别名", "L"}},
		{"produceAssert", []string{"接口类型断言", "动态别名", "L"}},
		{"produceFactory", []string{"运行时调用", "NewBus(cfg.Addr)", "实参：", "运行时配置字段"}},
	}
	for _, c := range cases {
		pf := parsedFuncFromSrc(t, runtimeBindingSrc, c.fn)
		at := callPosOf(t, pf, "Send")
		b := resolveBindingAt(pf, "b", at)
		if b.known {
			t.Fatalf("%s：运行时来源不应判为已证明（sig=%q）", c.fn, b.sig)
		}
		if b.unknownWhy == "" {
			t.Fatalf("%s：缺少未知原因", c.fn)
		}
		for _, w := range c.wants {
			if !strings.Contains(b.unknownWhy, w) {
				t.Fatalf("%s：原因 %q 缺少 %q", c.fn, b.unknownWhy, w)
			}
		}
		// bindingText 必须把定位后的原因带给用户
		if txt := bindingText(b); !strings.Contains(txt, strings.Split(c.wants[0], " ")[0]) {
			t.Fatalf("%s：bindingText=%q 未带来源", c.fn, txt)
		}
	}
}

func TestRuntimeBindingVerdictStaysUnverified(t *testing.T) {
	prod := parsedFuncFromSrc(t, runtimeBindingSrc, "produceConfig")
	cons := parsedFuncFromSrc(t, runtimeBindingSrc, "consumeLiteral")
	s := resolveBindingAt(prod, "b", callPosOf(t, prod, "Send"))
	r := resolveBindingAt(cons, "c", callPosOf(t, cons, "Receive"))
	same, verdict, detail := sameBinding(s, r)
	if same || verdict != "unverified" {
		t.Fatalf("运行时配置 vs 字面量：same=%v verdict=%s detail=%s", same, verdict, detail)
	}
	for _, w := range []string{"运行时配置字段", "cfg.Addr"} {
		if !strings.Contains(detail, w) {
			t.Fatalf("detail=%q 缺少 %q", detail, w)
		}
	}
}

func TestLiteralBindingStillCorroborated(t *testing.T) {
	prod := parsedFuncFromSrc(t, runtimeBindingSrc, "produceLiteral")
	cons := parsedFuncFromSrc(t, runtimeBindingSrc, "consumeLiteral")
	s := resolveBindingAt(prod, "p", callPosOf(t, prod, "Send"))
	r := resolveBindingAt(cons, "c", callPosOf(t, cons, "Receive"))
	if !s.known || !r.known {
		t.Fatalf("字面量配置应 known：sender=%+v receiver=%+v", s, r)
	}
	same, verdict, detail := sameBinding(s, r)
	if !same || verdict != SeqVerdictCorroborated {
		t.Fatalf("同配置字面量应 corroborated：same=%v verdict=%s detail=%s", same, verdict, detail)
	}
	if !strings.Contains(detail, "Addr=\"literal-addr\"") {
		t.Fatalf("detail=%q 未含配置签名", detail)
	}
}

func TestLiteralBindingMismatchStillDetected(t *testing.T) {
	prod := parsedFuncFromSrc(t, runtimeBindingSrc, "produceLiteral")
	other := parsedFuncFromSrc(t, runtimeBindingSrc, "consumeOther")
	s := resolveBindingAt(prod, "p", callPosOf(t, prod, "Send"))
	r := resolveBindingAt(other, "q", callPosOf(t, other, "Receive"))
	same, verdict, detail := sameBinding(s, r)
	if same || verdict != "mismatch" {
		t.Fatalf("不同配置字面量应 mismatch：same=%v verdict=%s detail=%s", same, verdict, detail)
	}
}

// 空字面量（&Bus{}）保持原语义：无显式配置 → 与运行时来源比较仍 unverified。
func TestEmptyLiteralBindingUnchanged(t *testing.T) {
	src := `package m

type Bus struct{ Addr string }

func a() {
	b := &Bus{}
	b.Send("t")
}

func b() {
	c := &Bus{}
	c.Receive("t")
}
`
	p := parsedFuncFromSrc(t, src, "a")
	q := parsedFuncFromSrc(t, src, "b")
	s := resolveBindingAt(p, "b", callPosOf(t, p, "Send"))
	r := resolveBindingAt(q, "c", callPosOf(t, q, "Receive"))
	if !s.known || s.sig != "{}" {
		t.Fatalf("空字面量应 known 且 sig={}：%+v", s)
	}
	if _, verdict, _ := sameBinding(s, r); verdict != "unverified" {
		t.Fatalf("空字面量双方无显式配置 → unverified，得到 %s", verdict)
	}
}
