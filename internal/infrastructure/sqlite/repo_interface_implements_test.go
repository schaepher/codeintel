package sqlite

// T16：接口 implements 邻接查询（只取 implements、稳定排序、未知接口空集、
// 取消错误）与注册函数投影一次读取。

import (
	"context"
	"sort"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestGetInterfaceImplementsOnlyImplements(t *testing.T) {
	r := newTestRepo(t)
	ifaceA := domain.CanonicalID("symbol:go:example.com/m:(API).Work")
	ifaceB := domain.CanonicalID("symbol:go:example.com/m:(Other).Work")
	save(t, r, []*domain.CodeEntity{
		node(string(ifaceA), string(domain.KindInterface), "API", "api.go"),
		node(string(ifaceB), string(domain.KindInterface), "Other", "api.go"),
		node("symbol:go:example.com/m:Two", string(domain.KindStruct), "Two", "two.go"),
		node("symbol:go:example.com/m:One", string(domain.KindStruct), "One", "one.go"),
		node("symbol:go:example.com/m:CallsOnly", string(domain.KindFunction), "CallsOnly", "x.go"),
	}, []*domain.Fact{
		{SourceID: ifaceA, TargetID: "symbol:go:example.com/m:Two", Kind: domain.FactImplements,
			ToolSource: domain.ToolSCIP, Confidence: 1.0},
		{SourceID: ifaceA, TargetID: "symbol:go:example.com/m:One", Kind: domain.FactImplements,
			ToolSource: domain.ToolSCIP, Confidence: 1.0},
		// 普通调用邻居不得混入（双向 Expand 会带出来的其它边）
		{SourceID: ifaceA, TargetID: "symbol:go:example.com/m:CallsOnly", Kind: domain.FactCalls,
			ToolSource: domain.ToolCodeGraph, Confidence: 0.8},
		{SourceID: ifaceB, TargetID: "symbol:go:example.com/m:One", Kind: domain.FactImplements,
			ToolSource: domain.ToolSCIP, Confidence: 1.0},
	})

	got, err := r.GetInterfaceImplements(context.Background(), string(ifaceA))
	if err != nil {
		t.Fatalf("GetInterfaceImplements: %v", err)
	}
	want := []string{"symbol:go:example.com/m:One", "symbol:go:example.com/m:Two"}
	if len(got) != len(want) {
		t.Fatalf("implements = %v, want %v", got, want)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Fatalf("implements = %v, want %v（需稳定排序、不含 calls 邻居）", got, want)
		}
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] < got[j] }) {
		t.Fatalf("结果未按 ID 稳定排序: %v", got)
	}

	// 接口 B 只含自身实现（不串接口 A 的候选）。
	gotB, err := r.GetInterfaceImplements(context.Background(), string(ifaceB))
	if err != nil {
		t.Fatalf("GetInterfaceImplements(B): %v", err)
	}
	if len(gotB) != 1 || string(gotB[0]) != "symbol:go:example.com/m:One" {
		t.Fatalf("接口 B 实现 = %v", gotB)
	}
}

func TestGetInterfaceImplementsUnknownInterfaceEmpty(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.GetInterfaceImplements(context.Background(), "symbol:go:example.com/m:NoSuch")
	if err != nil {
		t.Fatalf("未知接口不应报错: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("未知接口应为空集: %v", got)
	}
}

func TestGetInterfaceImplementsCanceled(t *testing.T) {
	r := newTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.GetInterfaceImplements(ctx, "symbol:go:example.com/m:(API).Work"); err == nil {
		t.Fatal("已取消 ctx 应返回取消错误")
	}
}

// 注册函数投影一次读取即可拿到全部服务（多服务不重复查属性）。
func TestGetRouteRegisterNodesAllInOneRead(t *testing.T) {
	r := newTestRepo(t)
	save(t, r, []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:RegisterSvcAServer", Kind: domain.KindFunction,
			Name: "RegisterSvcAServer", FilePath: "a.pb.go", LineStart: 10,
			Properties: map[string]any{"registers_service": "SvcA"}},
		{ID: "symbol:go:example.com/m:RegisterSvcBHandler", Kind: domain.KindFunction,
			Name: "RegisterSvcBHandler", FilePath: "b.pb.go", LineStart: 20,
			Properties: map[string]any{"registers_service": "SvcB"}},
		// 同名 helper 没有 registers_service 属性 → 不在投影里
		{ID: "symbol:go:example.com/m:RegisterSvcCHandler", Kind: domain.KindFunction,
			Name: "RegisterSvcCHandler", FilePath: "c.pb.go", LineStart: 30},
	}, nil)
	nodes, err := r.GetRouteRegisterNodes()
	if err != nil {
		t.Fatalf("GetRouteRegisterNodes: %v", err)
	}
	got := map[string]string{}
	for _, n := range nodes {
		got[n.Property("registers_service")] = n.FilePath
	}
	if len(got) != 2 || got["SvcA"] != "a.pb.go" || got["SvcB"] != "b.pb.go" {
		t.Fatalf("注册投影 = %v", got)
	}
	if _, ok := got["SvcC"]; ok {
		t.Fatalf("未注册同名 helper 不应进入注册投影: %v", got)
	}
}
