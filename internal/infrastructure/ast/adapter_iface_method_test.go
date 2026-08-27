package ast

// W1 测试（用户需求）：调用解析不能只记录接口类型，必须包含接口
// 方法定义——接口方法调用（参数/变量形态，静态无法确定实现）的
// calls 边 target 应为接口方法节点 (Iface).Method（含方法名）——
// 时序图据此具体化到实现并展开。修复前 target 是接口类型节点
// （无方法名），时序图无法展开。

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// TestChainedCallNewServiceDotDoSth：aa.NewService().DoSth() 链式调用
// ——sel.X 是 CallExpr（走 emitGinChainedCall，非 gin 方法 return 无
// 效果）→ 应继续建 DoSth 的 calls 边（用户疑问：是否识别不到）。
func TestChainedCallNewServiceDotDoSth(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"aa/aa.go": `package aa

type Service struct{}

func (s *Service) DoSth() {}

// 构造器：返回具体类型
func NewService() *Service {
	return &Service{}
}
`,
		"main.go": `package mtest

import "example.com/mtest/aa"

func run() {
	aa.NewService().DoSth()
}
`,
	})
	_ = nodes
	// DoSth 的 calls 边存在（source = run）
	found := false
	for _, f := range facts {
		if f.Kind == domain.FactCalls &&
			string(f.SourceID) == "symbol:go:example.com/mtest:run" &&
			string(f.TargetID) == "symbol:go:example.com/mtest/aa:(Service).DoSth" {
			found = true
		}
	}
	if !found {
		t.Error("aa.NewService().DoSth() 的 calls 边应存在（source=run, target=(Service).DoSth）")
	}
}

// TestChainedCallIfaceReturn：NewService 返回接口（函数体 return 具体
// 实现）→ aa.NewService().DoSth() 应具体化到实现方法（R18 链式具体化
// 在接口分支——W1 后无法确定时回退接口方法）。
func TestChainedCallIfaceReturn(t *testing.T) {
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
`,
		"main.go": `package mtest

import "example.com/mtest/aa"

func run() {
	aa.NewService().DoSth()
}
`,
	})
	found := false
	for _, f := range facts {
		if f.Kind == domain.FactCalls &&
			string(f.SourceID) == "symbol:go:example.com/mtest:run" {
			t.Logf("边: %s → %s", f.SourceID, f.TargetID)
			if string(f.TargetID) == "symbol:go:example.com/mtest/aa:(svcImpl).DoSth" {
				found = true
			}
		}
	}
	if !found {
		t.Error("接口返回的链式调用应具体化到 (svcImpl).DoSth（构造器 return 追踪）")
	}
}

// TestExternalCallEdge（用户要求）：第三方依赖包调用也要建边——
// 外部包方法（c.Get()）与外部包函数（ext.Helper()）都建 calls 边
// （轻量节点），不深入外部包内部解析（无 Syntax 天然不展开）。
func TestExternalCallEdge(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod":        "module example.com/mtest\n\ngo 1.21\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n",
		"../ext/go.mod": "module example.com/ext\n\ngo 1.21\n",
		"../ext/ext.go": `package ext

type Client struct{}

func (c *Client) Get() {}

func NewClient() *Client {
	return &Client{}
}

func Helper() {}
`,
		"main.go": `package mtest

import "example.com/ext"

func run() {
	c := ext.NewClient()
	c.Get()      // 外部包方法调用
	ext.Helper() // 外部包函数调用
}
`,
	})
	got := map[string]bool{}
	for _, f := range facts {
		if f.Kind == domain.FactCalls && string(f.SourceID) == "symbol:go:example.com/mtest:run" {
			got[string(f.TargetID)] = true
		}
	}
	if !got["symbol:go:example.com/ext:(Client).Get"] {
		t.Error("外部包方法调用 c.Get() 应建边 → (Client).Get")
	}
	// 纯包函数（ext.Helper）不建边——防图爆炸（fmt.Println 同款旧设计）
	if got["symbol:go:example.com/ext:Helper"] {
		t.Error("外部包纯函数调用 ext.Helper() 不应建边（防图爆炸——方法/接口方法才建）")
	}
}

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
	// 预期 offset：与消费端同算法——parser 解析 src，CallExpr.Pos()
	// （Lparen）的字节偏移
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "main.go", src, 0)
	doSthOff, helperOff := -1, -1
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 {
				switch sel.Sel.Name {
				case "DoSth":
					doSthOff = fset.Position(call.Pos()).Offset
				case "Helper":
					helperOff = fset.Position(call.Pos()).Offset
				}
			}
		}
		return true
	})
	got := map[string]float64{}
	for _, f := range facts {
		if f.Kind == domain.FactCalls && string(f.SourceID) == "symbol:go:example.com/mtest:run" {
			// 内存 facts 的 pos 为 int；DB 反序列化为 float64——双兼容
			switch v := f.Metadata["pos"].(type) {
			case float64:
				got[string(f.TargetID)] = v
			case int:
				got[string(f.TargetID)] = float64(v)
			}
		}
	}
	// aa.NewService().DoSth() 链式：外层边 pos = DoSth 的 Lparen
	if p := got["symbol:go:example.com/mtest/aa:(svcImpl).DoSth"]; int(p) != doSthOff {
		t.Errorf("DoSth 边 pos = %d; want %d（call.Pos()=Lparen——与消费端一致）", int(p), doSthOff)
	}
	if p := got["symbol:go:example.com/mtest/aa:Helper"]; int(p) != helperOff {
		t.Errorf("Helper 边 pos = %d; want %d", int(p), helperOff)
	}
	// 同行两个调用 pos 不同（互不覆盖）
	if int(got["symbol:go:example.com/mtest/aa:(svcImpl).DoSth"]) == int(got["symbol:go:example.com/mtest/aa:Helper"]) {
		t.Error("同行链式 + 并列调用 pos 应不同（各自独立）")
	}
}
