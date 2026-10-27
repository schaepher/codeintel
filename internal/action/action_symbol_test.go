package action

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestSymbolDetailDispatchCandidates 接口类型的符号详情必须自带候选实现
// （P1-03：cli/MCP/ask 三个消费者共用同一组合结果——原先只有 cli 自己
// 调 DispatchCandidates 拼装，MCP 的 symbol 工具描述承诺了 candidates
// 却不返回）。
func TestSymbolDetailDispatchCandidates(t *testing.T) {
	a, dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	ifaceID := domain.CanonicalID("symbol:go:example.com/m/svc:Handler")
	implID := domain.CanonicalID("symbol:go:example.com/m/svc:(Svc).Run")
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: ifaceID, Kind: domain.KindInterface, Name: "Handler", FilePath: "svc/svc.go", LineStart: 3},
		{ID: implID, Kind: domain.KindMethod, Name: "(Svc).Run", FilePath: "svc/svc.go", LineStart: 5},
	}, []*domain.Fact{{
		SourceID: ifaceID, TargetID: implID, Kind: domain.FactDispatchTo,
		ToolSource: domain.ToolSSA, Confidence: 0.9,
		Metadata: map[string]any{"origin": "register", "interface_method": "Handle",
			"register_site": float64(5), "register_file": "svc/reg.go", "confidence": 0.9},
	}}, nil); err != nil {
		t.Fatal(err)
	}

	d, err := a.SymbolDetail("Handler")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Dispatch) != 1 {
		t.Fatalf("dispatch = %d, want 1", len(d.Dispatch))
	}
	c := d.Dispatch[0]
	if c.Impl != implID || c.Method != "Handle" || c.Origin != "register" ||
		c.RegisterSite != 5 || c.RegisterFile != "svc/reg.go" || c.Confidence != 0.9 {
		t.Errorf("candidate = %+v", c)
	}

	// 非接口符号不产生派发候选（不查 dispatch_to，避免噪音/额外成本）
	d2, err := a.SymbolDetail("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(d2.Dispatch) != 0 {
		t.Errorf("main dispatch = %+v, want empty", d2.Dispatch)
	}
}
