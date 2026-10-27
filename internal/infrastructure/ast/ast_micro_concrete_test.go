package ast

// todo22 P2：go-micro 根包 `micro.RegisterHandler` 的具体类型实参（从
// ast_micro_test.go 拆出——行数治理）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestIndexMicroRootRegisterHandlerConcrete：根包 `micro.RegisterHandler(s,
// new(AccountServer))`——形参是 any，服务短名取**调用实参的具体类型名**
// （go-micro handler 名 = 类型名），方法集取指针方法，实现边照常发射。
func TestIndexMicroRootRegisterHandlerConcrete(t *testing.T) {
	files := microFixtureFiles()
	app := files["app/app.go"]
	app = strings.Replace(app, `"go-micro.dev/v2/client"`,
		`"go-micro.dev/v2/client"
	"go-micro.dev/v2/server"`, 1)
	app += `
func SetupRoot(s server.Server) {
	micro.RegisterHandler(s, new(AccountServer))
}
`
	files["app/app.go"] = app
	nodes, facts := indexFixture(t, files)

	svc := mergedNode(t, nodes, "symbol:go:example.com/mtest/app:svc.AccountServer")
	if svc.Kind != domain.KindGrpcService {
		t.Errorf("AccountServer kind = %s; want grpc_service", svc.Kind)
	}
	if got := svc.Property("rpc_protocol"); got != domain.RPCProtocolMicro {
		t.Errorf("rpc_protocol = %q; want micro", got)
	}
	if got := svc.Property("service_name"); got != "AccountServer" {
		t.Errorf("service_name = %q; want AccountServer（handler 名 = 具体类型名）", got)
	}
	if got := svc.Property("methods"); !strings.Contains(got, "Apply") {
		t.Errorf("methods = %q; want 含 Apply", got)
	}
	findFact(t, facts, "symbol:go:example.com/mtest/app:AccountServer",
		"symbol:go:example.com/mtest/app:svc.AccountServer", string(domain.FactGrpcImpl))

	// 注册函数属性（registers_service 打在 go-micro 根包 RegisterHandler 上）
	reg := mergedNode(t, nodes, "symbol:go:go-micro.dev/v2:RegisterHandler")
	if got := reg.Property("registers_service"); got != "AccountServer" {
		t.Errorf("registers_service = %q; want AccountServer", got)
	}
}

// TestIndexMicroRootRegisterHandlerDynamicArg：非具名/动态实参（interface{}）
// 不产生服务节点（不猜服务短名）。
func TestIndexMicroRootRegisterHandlerDynamicArg(t *testing.T) {
	files := microFixtureFiles()
	app := files["app/app.go"]
	app = strings.Replace(app, `"go-micro.dev/v2/client"`,
		`"go-micro.dev/v2/client"
	"go-micro.dev/v2/server"`, 1)
	app += `
func SetupRootDynamic(s server.Server, h any) {
	micro.RegisterHandler(s, h)
}
`
	files["app/app.go"] = app
	nodes, _ := indexFixture(t, files)
	for _, n := range nodes {
		if n.Kind == domain.KindGrpcService && strings.HasSuffix(string(n.ID), ":svc.h") {
			t.Errorf("动态实参不得猜服务身份，got %s", n.ID)
		}
	}
}
