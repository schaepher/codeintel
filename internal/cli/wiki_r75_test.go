package cli

// R75 测试：调用链接口方法具体化——target 是接口方法 → implements 边
// 找实现类型同名方法 → 替换 + 展开实现方法一级调用（时序图反映实际
// 执行逻辑而非接口跳板）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestQueryChainIfaceImpl：入口调接口方法 → steps 出现实现方法 + 实现
// 方法的一级调用（接口是跳板不占层数）。
func TestQueryChainIfaceImpl(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// 入口 (svc).Run → 接口方法 (IRepo).Get；实现 (repoImpl).Get → 调 helper
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/svc:(svc).Run", Kind: domain.KindMethod, Name: "(svc).Run", FilePath: "svc/svc.go"},
		{ID: "symbol:go:example.com/m/repo:IRepo", Kind: domain.KindInterface, Name: "IRepo", FilePath: "repo/iface.go"},
		{ID: "symbol:go:example.com/m/repo:(IRepo).Get", Kind: domain.KindMethod, Name: "(IRepo).Get", FilePath: "repo/iface.go"},
		{ID: "symbol:go:example.com/m/repo:repoImpl", Kind: domain.KindStruct, Name: "repoImpl", FilePath: "repo/impl.go"},
		{ID: "symbol:go:example.com/m/repo:(repoImpl).Get", Kind: domain.KindMethod, Name: "(repoImpl).Get", FilePath: "repo/impl.go"},
		{ID: "symbol:go:example.com/m/repo:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "repo/helper.go"},
	}, []*domain.Fact{
		// 调用链：svc.Run → IRepo.Get（静态类型接口）
		{SourceID: "symbol:go:example.com/m/svc:(svc).Run", TargetID: "symbol:go:example.com/m/repo:(IRepo).Get", Kind: domain.FactCalls, Confidence: 0.9},
		// implements：repoImpl 实现 IRepo
		{SourceID: "symbol:go:example.com/m/repo:IRepo", TargetID: "symbol:go:example.com/m/repo:repoImpl", Kind: domain.FactImplements, Confidence: 0.9},
		// 实现方法一级调用：repoImpl.Get → helper
		{SourceID: "symbol:go:example.com/m/repo:(repoImpl).Get", TargetID: "symbol:go:example.com/m/repo:helper", Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts := action.New(r)
	chain := acts.QueryChain("symbol:go:example.com/m/svc:(svc).Run")
	if chain == nil || len(chain.Steps) == 0 {
		t.Fatalf("chain 空: %+v", chain)
	}
	var calls []string
	for _, st := range chain.Steps {
		calls = append(calls, st.Caller+"→"+st.Callee)
	}
	joined := strings.Join(calls, "; ")
	// 实现方法应出现（替换接口跳板）
	if !strings.Contains(joined, "repoImpl).Get") {
		t.Errorf("时序应含实现方法 (repoImpl).Get（接口具体化）:\n%s", joined)
	}
	// 实现方法的一级调用应展开（helper）
	if !strings.Contains(joined, "helper") {
		t.Errorf("时序应含实现方法的一级调用 helper:\n%s", joined)
	}
}
