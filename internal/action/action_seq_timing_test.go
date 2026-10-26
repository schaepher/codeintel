package action

// todo10-9：代码级时序的阶段计时（符号解析 / 源码读取解析 / 递归展开 /
// 渲染）+ 展开规模 + 终止原因。诊断只写日志与 stderr（stdout 的 JSON
// 契约不变）；主动终止（取消）不得报告为成功。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seqTimingFixture 建一个两层调用链的临时仓库：
// main → service.Run（第一层）→ repo.Load（第二层）。
func seqTimingFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type Repo struct{}

func (r *Repo) Load() error { return nil }

type Service struct {
	repo *Repo
}

func (s *Service) Run() error {
	if s.repo == nil {
		return nil
	}
	return s.repo.Load()
}

func main() {
	s := &Service{repo: &Repo{}}
	_ = s.Run()
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 18},
		{ID: "symbol:go:example.com/m:(Service).Run", Kind: domain.KindMethod, Name: "(Service).Run", FilePath: "main.go", LineStart: 11},
		{ID: "symbol:go:example.com/m:(Repo).Load", Kind: domain.KindMethod, Name: "(Repo).Load", FilePath: "main.go", LineStart: 5},
	}
	// 调用边（line_num 元数据——时序按调用点行号对齐被调符号）
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m:(Service).Run",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
			Metadata: map[string]any{"line_num": float64(20)}},
		{SourceID: "symbol:go:example.com/m:(Service).Run", TargetID: "symbol:go:example.com/m:(Repo).Load",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
			Metadata: map[string]any{"line_num": float64(15)}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCodeSequenceTimingStages：depth 1 与 depth 2 记录同一组阶段字段；
// depth 2 展开规模不小于 depth 1；终止原因（depth 上限）可见。
func TestCodeSequenceTimingStages(t *testing.T) {
	dir := seqTimingFixture(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := New(sqlite.NewRepo(db))

	run := func(depth int) (*CodeSeqNode, *SeqTiming) {
		root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:main", RepoAbs: dir, Depth: depth})
		if err != nil {
			t.Fatalf("depth=%d CodeSequence: %v", depth, err)
		}
		if root == nil || root.Meta == nil || root.Meta.Timing == nil {
			t.Fatalf("depth=%d 应返回根节点并携带 meta.timing（展开规模/阶段耗时）", depth)
		}
		return root, root.Meta.Timing
	}

	r1, t1 := run(1)
	r2, t2 := run(2)
	_, t3 := run(3)
	for name, tt := range map[string]*SeqTiming{"depth1": t1, "depth2": t2, "depth3": t3} {
		if tt.ResolveMS < 0 || tt.ReadMS < 0 || tt.WalkMS < 0 {
			t.Errorf("%s 阶段耗时不得为负: %+v", name, tt)
		}
		if tt.Nodes <= 0 {
			t.Errorf("%s 应记录展开规模（nodes）: %+v", name, tt)
		}
		if tt.Depth == 0 {
			t.Errorf("%s 应记录展开深度: %+v", name, tt)
		}
		if tt.Stop == "" {
			t.Errorf("%s 应记录终止原因: %+v", name, tt)
		}
		// 阶段互不重叠：total ≈ resolve+read+walk（允许 0.01ms 浮点误差）
		if sum := tt.ResolveMS + tt.ReadMS + tt.WalkMS; tt.TotalMS+0.01 < sum {
			t.Errorf("%s total 应覆盖各阶段之和（total=%.3f sum=%.3f）: %+v", name, tt.TotalMS, sum, tt)
		}
	}
	if t2.Nodes < t1.Nodes || t3.Nodes < t2.Nodes {
		t.Errorf("展开规模应随 depth 单调不减: d1=%d d2=%d d3=%d", t1.Nodes, t2.Nodes, t3.Nodes)
	}
	if t2.Depth != 2 || t1.Depth != 1 {
		t.Errorf("深度记录错: depth1=%d depth2=%d", t1.Depth, t2.Depth)
	}
	// 三层链 main → Run → Load：depth1/2 仍有可展开调用点（截断），
	// depth3 走完 → 自然结束。终止原因必须区分"分析停止"与"流程结束"。
	if t1.Stop != SeqStopDepth {
		t.Errorf("depth1 应记录 %q（上限截断），实为 %q", SeqStopDepth, t1.Stop)
	}
	if t2.Stop != SeqStopDepth {
		t.Errorf("depth2 应记录 %q（Load 内部未展开），实为 %q", SeqStopDepth, t2.Stop)
	}
	if t3.Stop != SeqStopNone {
		t.Errorf("depth3 应记录 %q（链展开完），实为 %q", SeqStopNone, t3.Stop)
	}
	if countSeqNodes(r2) <= countSeqNodes(r1) {
		t.Errorf("depth2 节点数应多于 depth1: %d vs %d", countSeqNodes(r2), countSeqNodes(r1))
	}
}

// TestCodeSequenceCanceledStopsQuickly：ctx 取消 → 及时返回错误（不报告
// 成功），终止原因记录为 canceled。
func TestCodeSequenceCanceledStopsQuickly(t *testing.T) {
	dir := seqTimingFixture(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := New(sqlite.NewRepo(db))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消：展开不得继续
	start := time.Now()
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:main", RepoAbs: dir, Depth: 2, Ctx: ctx})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrSeqCanceled) {
		t.Fatalf("取消应返回 ErrSeqCanceled，实为 root=%v err=%v", root, err)
	}
	if root != nil {
		t.Errorf("取消不应返回成功结果树: %+v", root.Meta)
	}
	if elapsed > 2*time.Second {
		t.Errorf("取消后应及时返回，实际耗时 %s", elapsed)
	}
}

// TestCountSeqNodes：展开规模统计覆盖分支/else/循环/嵌套。
func TestCountSeqNodes(t *testing.T) {
	root := &CodeSeqNode{Kind: "call", Nodes: []*CodeSeqNode{
		{Kind: "call"},
		{Kind: "branch", Nodes: []*CodeSeqNode{{Kind: "call"}}, Else: []*CodeSeqNode{{Kind: "loop", Nodes: []*CodeSeqNode{{Kind: "return"}}}}},
	}}
	if got := countSeqNodes(root); got != 6 {
		t.Errorf("countSeqNodes = %d; want 6", got)
	}
	if got := countSeqNodes(nil); got != 0 {
		t.Errorf("countSeqNodes(nil) = %d; want 0", got)
	}
}

// todo13-17：sequence 阶段计数（符号/SQL/签名/派发/缓存）在 meta.timing 可见。
func TestSeqStageCounters(t *testing.T) {
	acts, dir := seedSceneReachRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:Reassign", RepoAbs: dir, Depth: 3})
	if err != nil || root == nil || root.Meta == nil || root.Meta.Timing == nil {
		t.Fatalf("应输出 timing: %v", err)
	}
	tm := root.Meta.Timing
	if tm.SymLookups == 0 || tm.SQLQueries == 0 {
		t.Errorf("应有符号/SQL 次数: %+v", tm)
	}
	if tm.CacheMisses == 0 {
		t.Errorf("首次展开应统计源码缓存未命中: %+v", tm)
	}
	// 同文件多函数展开：第二次读取命中缓存
	root2, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:ConditionalReassign", RepoAbs: dir, Depth: 3})
	if err != nil || root2 == nil || root2.Meta == nil || root2.Meta.Timing == nil {
		t.Fatal(err)
	}
	if root2.Meta.Timing.CacheHits == 0 {
		t.Errorf("同文件多函数应命中源码缓存: %+v", root2.Meta.Timing)
	}
}
