package cli

// R76 测试：query sequence 命令（时序图——接口具体化后反映实际执行；
// wiki 流程页同款数据源 CalleesConcrete）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestQuerySequenceCmd：命令输出含实现方法（接口具体化）与入口。
func TestQuerySequenceCmd(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/svc:(svc).Run", Kind: domain.KindMethod, Name: "(svc).Run", FilePath: "svc/svc.go"},
		{ID: "symbol:go:example.com/m/repo:IRepo", Kind: domain.KindInterface, Name: "IRepo", FilePath: "repo/iface.go"},
		{ID: "symbol:go:example.com/m/repo:repoImpl", Kind: domain.KindStruct, Name: "repoImpl", FilePath: "repo/impl.go"},
		{ID: "symbol:go:example.com/m/repo:(repoImpl).Get", Kind: domain.KindMethod, Name: "(repoImpl).Get", FilePath: "repo/impl.go"},
		{ID: "symbol:go:example.com/m/repo:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "repo/helper.go"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m/svc:(svc).Run", TargetID: "symbol:go:example.com/m/repo:IRepo", Kind: domain.FactCalls, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/repo:IRepo", TargetID: "symbol:go:example.com/m/repo:repoImpl", Kind: domain.FactImplements, Confidence: 0.9},
		{SourceID: "symbol:go:example.com/m/repo:(repoImpl).Get", TargetID: "symbol:go:example.com/m/repo:helper", Kind: domain.FactCalls, Confidence: 0.9},
	}, nil); err != nil {
		t.Fatal(err)
	}
	_ = action.New(r)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "(svc).Run", "--repo", dir}); code != 0 {
			t.Fatalf("cmdQuery sequence = %d; want 0", code)
		}
	})
	for _, want := range []string{"时序", "(svc).Run", "repoImpl"} {
		if !strings.Contains(out, want) {
			t.Errorf("sequence 输出应含 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "IRepo") {
		t.Errorf("接口应被具体化（不应出现 IRepo）:\n%s", out)
	}
}
