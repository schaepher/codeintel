package action

// R9x 测试（迁自 cli/query_wiki_test.go 的逻辑断言部分）：Module——
// 模块详情装配（查找/核心符号展示串/关键数据流/包间调用）；模块
// 架构图 mermaid 是 cli 渲染，不入 action。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestModule：完整路径/短名查找 + 详情装配（KeyFlows 走真实
// WikiKeyFlows——seedRepo 的 main 有 T.A 读写摘要）。
func TestModule(t *testing.T) {
	a, _ := seedRepo(t)
	data := []*domain.WikiModule{
		{Name: "example.com/m", ShortName: "m", Desc: "业务入口",
			Entries: []string{"main"},
			CoreSymbols: []*domain.WikiSymbol{
				{ID: "symbol:go:example.com/m:main", Name: "main", Kind: "function", Callers: 2},
				{ID: "symbol:go:example.com/m/svc:(Svc).Run", Name: "(Svc).Run", Kind: "method", Callers: 1},
			},
			OutCalls: []string{"example.com/m/svc"}, InCalls: []string{"util"},
			Tables:   []string{"orders"},
			PkgCalls: []*domain.WikiPkgCall{{From: "m", To: "svc", Count: 3}},
		},
		{Name: "example.com/m/util", ShortName: "util"},
	}
	out, err := a.Module(ModuleRequest{Data: data, Name: "example.com/m"})
	if err != nil || out == nil {
		t.Fatalf("Module = %v, %v", out, err)
	}
	if out.Name != "example.com/m" || out.ShortName != "m" || out.Desc != "业务入口" {
		t.Errorf("name/short/desc = %q/%q/%q", out.Name, out.ShortName, out.Desc)
	}
	if len(out.Entries) != 1 || out.Entries[0] != "main" {
		t.Errorf("entries = %v", out.Entries)
	}
	if len(out.CoreSymbols) != 2 || out.CoreSymbols[0] != "main（function，2 调用者）" {
		t.Errorf("core_symbols = %v", out.CoreSymbols)
	}
	// KeyFlows：main 的 T.A 读写摘要（模块前缀过滤）
	if len(out.KeyFlows) != 1 || out.KeyFlows[0].Symbol != "main" {
		t.Fatalf("key_flows = %+v", out.KeyFlows)
	}
	if len(out.KeyFlows[0].Reads) != 1 || len(out.KeyFlows[0].Writes) != 1 {
		t.Errorf("key_flows reads/writes = %v/%v", out.KeyFlows[0].Reads, out.KeyFlows[0].Writes)
	}
	if len(out.OutCalls) != 1 || out.OutCalls[0] != "example.com/m/svc" {
		t.Errorf("out_calls = %v", out.OutCalls)
	}
	if len(out.InCalls) != 1 || out.InCalls[0] != "util" {
		t.Errorf("in_calls = %v", out.InCalls)
	}
	if len(out.Tables) != 1 || out.Tables[0] != "orders" {
		t.Errorf("tables = %v", out.Tables)
	}
	if len(out.PkgCalls) != 1 || out.PkgCalls[0].From != "m" || out.PkgCalls[0].Count != 3 {
		t.Errorf("pkg_calls = %+v", out.PkgCalls)
	}
}

// TestModuleShortName：短名查找命中；未找到返回 (nil, nil)。
func TestModuleShortName(t *testing.T) {
	a, _ := seedRepo(t)
	data := []*domain.WikiModule{
		{Name: "example.com/m", ShortName: "m"},
		{Name: "example.com/m/util", ShortName: "util"},
	}
	out, err := a.Module(ModuleRequest{Data: data, Name: "util"})
	if err != nil || out == nil || out.Name != "example.com/m/util" {
		t.Errorf("short name lookup = %v, %v", out, err)
	}
	out, err = a.Module(ModuleRequest{Data: data, Name: "notexist"})
	if err != nil || out != nil {
		t.Errorf("missing module = %v, %v; want (nil, nil)", out, err)
	}
}

// TestModuleNoSymbols：无 CoreSymbols 时 KeyFlows 为空（不查库）。
func TestModuleNoSymbols(t *testing.T) {
	a, _ := seedRepo(t)
	data := []*domain.WikiModule{{Name: "example.com/m", ShortName: "m"}}
	out, err := a.Module(ModuleRequest{Data: data, Name: "m"})
	if err != nil || out == nil {
		t.Fatalf("Module = %v, %v", out, err)
	}
	if out.KeyFlows != nil {
		t.Errorf("key_flows = %+v; want nil", out.KeyFlows)
	}
}
