//go:build integration

package integration

// todo29 T09b 夹具：生成注册 helper 直接接收具体复合字面量（公开本地 stub）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const t29bMicroClient = `package client
import "context"
type Request interface{}
type CallOption interface{}
type RequestOption interface{}
type Client interface { NewRequest(string,string,interface{},...RequestOption) Request; Call(context.Context,Request,interface{},...CallOption) error }
`

const t29bMicroServer = `package server
type HandlerOption interface{}
type Handler interface{}
type Server interface { NewHandler(interface{},...HandlerOption) Handler; Handle(Handler) error }
`

const t29bWire = `package wire
import("context"; "go-micro.dev/v4/client"; "go-micro.dev/v4/server")
type Request struct { ID string }
type Response struct{}
type LedgerService interface { Submit(context.Context,*Request,...client.CallOption)(*Response,error) }
type ledgerService struct { c client.Client; name string }
func NewLedgerService(name string,c client.Client) LedgerService { return &ledgerService{c:c,name:name} }
func(c *ledgerService) Submit(ctx context.Context,in *Request,opts ...client.CallOption)(*Response,error) {
 req:=c.c.NewRequest(c.name,"Ledger.Submit",in)
 out:=new(Response)
 err:=c.c.Call(ctx,req,out,opts...)
 return out,err
}
type LedgerHandler interface { Submit(context.Context,*Request,*Response)error }
func RegisterLedgerHandler(s server.Server,h LedgerHandler,opts ...server.HandlerOption)error {
 type Ledger struct { LedgerHandler }
 return s.Handle(s.NewHandler(&Ledger{h},opts...))
}
`

const t29bEntry = `package flow
import("context"; "example.com/flow/wire")
func Entry(ctx context.Context) error {
 c:=wire.NewLedgerService("local",nil)
 _,err:=c.Submit(ctx,&wire.Request{ID:"record-1"})
 return err
}
`

const t29bCallbacks = `package callbacks
import "context"
type Decorator func(context.Context,func(context.Context)error)func(context.Context)error
`

const t29bFrameworkDB = `package db
func Within(work func()error)error{return work()}
`

// t29bHandler 服务端注册 + DI/AOP/装饰器。
func t29bHandler(registerLine string) string {
	return `package handler
import("context"; "example.com/flow/wire"; "go-micro.dev/v4/server"; "example.com/framework/db"; decorators "example.com/flow/callbacks")
type Work interface{ Execute(context.Context,*wire.Request)error }
type Handler struct{work Work}
type Aspect struct{inner Work}
type Worker struct{}
` + registerLine + `
func(h *Handler) Submit(ctx context.Context,in *wire.Request,out *wire.Response)error { return h.work.Execute(ctx,in) }
func(a *Aspect) Execute(ctx context.Context,in *wire.Request)error {
 return Run(ctx,func(ctx context.Context)error{return a.inner.Execute(ctx,in)},FirstFactory(),SecondFactory())
}
func(w *Worker) Execute(ctx context.Context,in *wire.Request)error {
 defer func(){Cleanup(in.ID)}()
 return Save(in.ID)
}
func FirstFactory()decorators.Decorator{FactoryFirst();return First}
func SecondFactory()decorators.Decorator{FactorySecond();return Second}
func First(ctx context.Context,f func(context.Context)error)func(context.Context)error {
 return func(ctx context.Context)error{BeforeFirst();err:=f(ctx);AfterFirst();return err}
}
func Second(ctx context.Context,f func(context.Context)error)func(context.Context)error {
 return func(ctx context.Context)error{BeforeSecond();err:=f(ctx);AfterSecond();return err}
}
func Run(ctx context.Context,process func(context.Context)error,wrappers ...decorators.Decorator)error {
 for _,wrapper:=range wrappers{process=wrapper(ctx,process)}
 return db.Within(func()error{return process(ctx)})
}
func Save(id string)error{return nil}
func Cleanup(id string){}
func FactoryFirst(){}
func FactorySecond(){}
func BeforeFirst(){}
func AfterFirst(){}
func BeforeSecond(){}
func AfterSecond(){}
var Bypass bool
func ConditionalEntry(ctx context.Context)error {
 process:=Conditional(func(context.Context)error{return Save("conditional")})
 return process(ctx)
}
func Conditional(f func(context.Context)error)func(context.Context)error {
 if Bypass{return f}
 return func(ctx context.Context)error{BeforeFirst();return f(ctx)}
}
`
}

const t29bRegisterOK = `func Register(s server.Server)error { return wire.RegisterLedgerHandler(s,&Handler{work:&Aspect{inner:&Worker{}}}) }`
const t29bRegisterMissing = ``
const t29bRegisterBadType = `type NotHandler struct{}
func Register(s server.Server)error { return wire.RegisterLedgerHandler(s,&NotHandler{}) }`
const t29bRegisterConflict = `type ConflictHandler struct{}
func(h *ConflictHandler) Submit(ctx context.Context,in *wire.Request,out *wire.Response)error{return nil}
func Register(s server.Server)error { return wire.RegisterLedgerHandler(s,&Handler{work:&Aspect{inner:&Worker{}}}) }
func RegisterConflict(s server.Server)error { return wire.RegisterLedgerHandler(s,&ConflictHandler{}) }`

func t29bFixture(t *testing.T, registerLine string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "framework", "go.mod"), "module example.com/framework\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "framework", "db", "transaction.go"), t29bFrameworkDB)
	app := filepath.Join(root, "app")
	writeFile(t, filepath.Join(app, "go.mod"), `module example.com/flow

go 1.25
require (
go-micro.dev/v4 v4.0.0
example.com/framework v0.0.0
)
replace go-micro.dev/v4 => ./micro
replace example.com/framework => ../framework
`)
	writeFile(t, filepath.Join(app, "micro", "go.mod"), "module go-micro.dev/v4\n\ngo 1.25\n")
	writeFile(t, filepath.Join(app, "micro", "client", "client.go"), t29bMicroClient)
	writeFile(t, filepath.Join(app, "micro", "server", "server.go"), t29bMicroServer)
	writeFile(t, filepath.Join(app, "wire", "ledger.pb.micro.go"), t29bWire)
	writeFile(t, filepath.Join(app, "entry.go"), t29bEntry)
	writeFile(t, filepath.Join(app, "callbacks", "types.go"), t29bCallbacks)
	writeFile(t, filepath.Join(app, "handler", "handler.go"), t29bHandler(registerLine))
	gitRun(t, app, "init")
	gitRun(t, app, "add", ".")
	gitRun(t, app, "commit", "-m", "fixture")
	clearIndex(t, app)
	if code := runCLI(t, "init", "--repo", app, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return app
}

type t29bSummary struct {
	Stop         any
	ServerNodes  int
	Save         int
	Cleanup      int
	CleanupExec  string
	Order        []string
	RCPSymbol    string
	RegisterFile string
	RegisterLine int
}

func t29bQuery(t *testing.T, dir string, depth string, extra ...string) t29bSummary {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/flow:Entry",
		"--repo", dir, "--code", "--depth", depth, "--evidence", "--json"}
	args = append(args, extra...)
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	s := t29bSummary{}
	countLabel := func(label string) int {
		n := 0
		var walk func(m map[string]any)
		walk = func(m map[string]any) {
			if m["Kind"] == "call" && m["Label"] == label {
				n++
			}
			for _, key := range []string{"Nodes", "Else"} {
				if arr, ok := m[key].([]any); ok {
					for _, c := range arr {
						if cm, ok := c.(map[string]any); ok {
							walk(cm)
						}
					}
				}
			}
		}
		for _, c := range doc["Nodes"].([]any) {
			walk(c.(map[string]any))
		}
		return n
	}
	var walk func(m map[string]any)
	walk = func(m map[string]any) {
		if ev, ok := m["evidence"].(map[string]any); ok && ev["source"] == "rpc_server" {
			// 只数真实服务端方法（Handler.Submit）；未注册时保留的客户端
			// 目标证据不算路由。
			if sym, _ := ev["symbol_id"].(string); strings.Contains(sym, ":(Handler).") {
				s.ServerNodes++
				s.RCPSymbol = sym
				s.RegisterFile, _ = ev["di_register_file"].(string)
				if v, ok := ev["di_register_line"].(float64); ok {
					s.RegisterLine = int(v)
				}
			}
		}
		if m["Kind"] == "call" {
			switch m["Label"] {
			case "FactoryFirst", "FactorySecond", "BeforeSecond", "BeforeFirst",
				"Save", "AfterFirst", "AfterSecond":
				s.Order = append(s.Order, m["Label"].(string))
			}
			if m["Label"] == "Cleanup" {
				if v, ok := m["exec"].(string); ok {
					s.CleanupExec = v
				}
			}
		}
		// 只按验收契约读取客户端调用节点（c.Submit）的 stop；空实现叶子函数
		// 的 no_calls 不是 RPC 续接失败。
		if m["Label"] == "c.Submit" {
			if stop, ok := m["stop"].(map[string]any); ok {
				s.Stop = stop["code"]
			}
		}
		for _, key := range []string{"Nodes", "Else"} {
			if arr, ok := m[key].([]any); ok {
				for _, c := range arr {
					if cm, ok := c.(map[string]any); ok {
						walk(cm)
					}
				}
			}
		}
	}
	for _, c := range doc["Nodes"].([]any) {
		walk(c.(map[string]any))
	}
	s.Save = countLabel("Save")
	s.Cleanup = countLabel("Cleanup")
	return s
}

func t29bDump(t *testing.T, dir, depth string, extra ...string) string {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/flow:Entry",
		"--repo", dir, "--code", "--depth", depth, "--evidence", "--json"}
	args = append(args, extra...)
	_, out := runCLIOut(t, args...)
	return out
}
