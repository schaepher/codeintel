package action

// R95 测试（迁自 cli/query_sequence_stop_test.go）：停止包判定 +
// 签名解析 + 调用参与者提取（纯逻辑——无 repo 依赖）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// TestSeqStopPkgHit：包匹配（完整路径/短名/后缀）。
func TestSeqStopPkgHit(t *testing.T) {
	stops := []string{"example.com/m/repo", "infra"}
	cases := map[string]bool{
		"symbol:go:example.com/m/repo:(R).Get":    true,  // 完整路径
		"symbol:go:example.com/m/repo:helper":     true,  // 完整路径
		"symbol:go:example.com/m/pkg/infra:(X).Y": true,  // 短名
		"symbol:go:example.com/m/svc:(S).Run":     false, // 未命中
		"symbol:go:example.com/m/order:(O).Pay":   false,
	}
	for id, want := range cases {
		if got := seqStopPkgHit(id, stops); got != want {
			t.Errorf("seqStopPkgHit(%s) = %v; want %v", id, got, want)
		}
	}
}

// TestSeqStopPkgEmpty：无停止包 → 不命中。
func TestSeqStopPkgEmpty(t *testing.T) {
	if seqStopPkgHit("symbol:go:example.com/m/repo:(R).Get", nil) {
		t.Error("无停止包不应命中")
	}
}

// TestImplTypeShort：短类型名提取（包末段.类型名——参与者第二行）。
func TestImplTypeShort(t *testing.T) {
	cases := map[string]string{
		"symbol:go:example.com/m/domain/order:(orderManagerImpl).SubmitOrder": "order.orderManagerImpl", // 方法形态
		"symbol:go:example.com/m/repo:OrderRepoImpl":                          "repo.OrderRepoImpl",     // 类型形态
		"symbol:go:example.com/m/svc:helper":                                  "svc.helper",             // 函数形态（无类型语义——短名）
		"symbol:go:example.com/m:(Svc).Run":                                   "m.Svc",                  // 根包方法
	}
	for id, want := range cases {
		if got := implTypeShort(id); got != want {
			t.Errorf("implTypeShort(%s) = %q; want %q", id, got, want)
		}
	}
}

// TestParseSigTypes：签名解析——参数/返回类型短名（R83：消息线参数 +
// return 线）。
func TestParseSigTypes(t *testing.T) {
	sig := `func (*orderManagerImpl).SubmitOrder(data github.com/ixre/go2o/pkg/interface/domain/order.SubmitOrderData) (github.com/ixre/go2o/pkg/interface/domain/order.IOrder, *github.com/ixre/go2o/pkg/interface/domain/order.SubmitReturnData, error)`
	args, rets, ok := parseSigTypes(sig)
	if !ok {
		t.Fatal("签名解析失败")
	}
	if len(args) != 1 || args[0] != "order.SubmitOrderData" {
		t.Errorf("args = %v; want [order.SubmitOrderData]", args)
	}
	if len(rets) != 3 || rets[0] != "order.IOrder" || rets[1] != "*order.SubmitReturnData" || rets[2] != "error" {
		t.Errorf("rets = %v; want [order.IOrder *order.SubmitReturnData error]", rets)
	}
	// 多参数 + 基础类型
	sig2 := `func Load(a, b int, s string) (bool, error)`
	args2, rets2, ok2 := parseSigTypes(sig2)
	if !ok2 || len(args2) != 3 || args2[0] != "int" || args2[1] != "int" || args2[2] != "string" {
		t.Errorf("args2 = %v ok=%v", args2, ok2)
	}
	if len(rets2) != 2 || rets2[0] != "bool" || rets2[1] != "error" {
		t.Errorf("rets2 = %v", rets2)
	}
}

// TestCallActor：调用参与者提取（对象而非方法）。
func TestCallActor(t *testing.T) {
	dir := t.TempDir()
	src := `package m

func Run() {
	s.manager.SubmitOrder()
	t.repo.CreateOrder()
	ic.Put()
	parser.NewPostedData()
	helper()
}
`
	fpath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(fpath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, fpath, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			want[callLabel(fset, []byte(src), call.Fun)] = callActor(fset, []byte(src), call.Fun)
		}
		return true
	})
	cases := map[string]string{
		"s.manager.SubmitOrder": "s.manager",
		"t.repo.CreateOrder":    "t.repo",
		"ic.Put":                "ic",
		"parser.NewPostedData":  "parser",
		"helper":                "helper",
	}
	for label, actor := range cases {
		if want[label] != actor {
			t.Errorf("callActor(%s) = %q; want %q", label, want[label], actor)
		}
	}
}
