package action

// todo12-5：异步关联的节点和关系必须以查询种子为范围。
//   - 无关模块（不共享字段/调用链）不得进入 nodes/links
//   - 无关联 → seed + 明确诊断（不是空成功）
//   - 候选预算稳定截断，实际数量/预算/partial 可审计
//   - 共享字段仍是 unverified（不提升为已验证因果）

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedAsyncScopeRepo(t *testing.T, nodes []*domain.CodeEntity, fields []*domain.FunctionFieldSummary,
	facts []*domain.Fact) (string, *Actions) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats(nodes, facts, fields); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "asyncscope", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir, New(r)
}

func asyncFunc(id, name string, line int) *domain.CodeEntity {
	return &domain.CodeEntity{ID: domain.CanonicalID(id), Kind: domain.KindFunction, Name: name,
		FilePath: "flow.go", LineStart: line}
}

func asyncWrite(fn, field string) *domain.FunctionFieldSummary {
	return &domain.FunctionFieldSummary{FunctionID: domain.CanonicalID(fn),
		AccessKind: domain.SummaryDirectWrite, FieldPath: field, LineStart: 1}
}

func asyncRead(fn, field string) *domain.FunctionFieldSummary {
	return &domain.FunctionFieldSummary{FunctionID: domain.CanonicalID(fn),
		AccessKind: domain.SummaryDirectRead, FieldPath: field, LineStart: 1}
}

// A1：共享申请身份（RecordID）的定时入口进入结果；无关模块不进入。
func TestAsyncScopeExcludesUnrelated(t *testing.T) {
	review := "symbol:go:example.com/app/review:(ReviewHandler).AfterReview"
	jobs := "symbol:go:example.com/app/jobs:Advance"
	rel := "symbol:go:example.com/app/unrelated:(Unrelated).Store"
	relRead := "symbol:go:example.com/app/unrelated:(Unrelated).Read"
	nodes := []*domain.CodeEntity{
		asyncFunc(review, "(ReviewHandler).AfterReview", 5),
		asyncFunc(jobs, "Advance", 9),
		asyncFunc(rel, "(Unrelated).Store", 20),
		asyncFunc(relRead, "(Unrelated).Read", 24),
	}
	fields := []*domain.FunctionFieldSummary{
		asyncWrite(review, "example.com/app/review.Job.RecordID"),
		asyncWrite(review, "example.com/app/review.Review.State"),
		asyncWrite(jobs, "example.com/app/review.Job.RecordID"),
		asyncWrite(rel, "example.com/app/unrelated.Unrelated.Cache"),
		asyncRead(relRead, "example.com/app/unrelated.Unrelated.Cache"),
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(review), TargetID: domain.CanonicalID("symbol:go:example.com/app/review:Decide"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 6}},
	}
	dir, acts := seedAsyncScopeRepo(t, nodes, fields, facts)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: review, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range res.Nodes {
		ids[n.ID] = true
	}
	if !ids[review] {
		t.Errorf("seed 必须保留: %+v", res.Nodes)
	}
	if !ids[jobs] {
		t.Errorf("共享申请身份的定时入口应进入节点: %+v", res.Nodes)
	}
	for id := range ids {
		if strings.Contains(id, "unrelated") {
			t.Errorf("无关模块不得进入结果: %s", id)
		}
	}
	for _, l := range res.Links {
		if strings.Contains(l.From, "unrelated") || strings.Contains(l.To, "unrelated") {
			t.Errorf("无关模块不得进入关系: %+v", l)
		}
		if !ids[l.From] || !ids[l.To] {
			t.Errorf("关系端点必须来自同一节点集合: %+v", l)
		}
	}
	// 共享字段不能冒充已验证因果
	for _, l := range res.Links {
		if l.Reason == "field_shared" && l.Verdict != SeqVerdictUnverified {
			t.Errorf("共享字段必须保持 unverified: %+v", l)
		}
	}
}

// A2：无证据关联 → seed + no_related_links 诊断。
func TestAsyncScopeNoLinks(t *testing.T) {
	seed := "symbol:go:example.com/app/review:AfterReview"
	other := "symbol:go:example.com/app/other:Store"
	nodes := []*domain.CodeEntity{
		asyncFunc(seed, "AfterReview", 5),
		asyncFunc(other, "Store", 9),
	}
	fields := []*domain.FunctionFieldSummary{
		asyncWrite(seed, "example.com/app/review.Review.State"),
		asyncWrite(other, "example.com/app/other.Cache.Data"),
	}
	dir, acts := seedAsyncScopeRepo(t, nodes, fields, nil)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: seed, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Links) != 0 {
		t.Errorf("无证据关联不得填充关系: %+v", res.Links)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "no_related_links" {
			found = true
		}
	}
	if !found {
		t.Errorf("应明确说明未发现关联: %+v", res.Diagnostics)
	}
	for _, n := range res.Nodes {
		if n.ID != seed {
			t.Errorf("无关联时节点应只有 seed: %+v", res.Nodes)
		}
	}
}

// A3：候选预算——稳定排序、实际数量、预算与 partial。
func TestAsyncScopeCandidateBudget(t *testing.T) {
	seed := "symbol:go:example.com/app:Seed"
	nodes := []*domain.CodeEntity{asyncFunc(seed, "Seed", 3)}
	fields := []*domain.FunctionFieldSummary{
		asyncWrite(seed, "example.com/app.Job.RecordID"),
	}
	for i := 0; i < asyncCandidateBudget+10; i++ {
		id := fmt.Sprintf("symbol:go:example.com/app:F%03d", i)
		nodes = append(nodes, asyncFunc(id, fmt.Sprintf("F%03d", i), 10+i))
		fields = append(fields, asyncRead(id, "example.com/app.Job.RecordID"))
	}
	dir, acts := seedAsyncScopeRepo(t, nodes, fields, nil)
	res, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: seed, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.CandidateTotal != asyncCandidateBudget+10 {
		t.Errorf("实际候选数 = %d; want %d", res.CandidateTotal, asyncCandidateBudget+10)
	}
	if res.CandidateBudget != asyncCandidateBudget || !res.Partial {
		t.Errorf("预算/partial 应可见: total=%d budget=%d partial=%v",
			res.CandidateTotal, res.CandidateBudget, res.Partial)
	}
	if len(res.Nodes) != asyncCandidateBudget+1 { // seed + 预算内候选
		t.Errorf("节点数 = %d; want %d", len(res.Nodes), asyncCandidateBudget+1)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "candidates_truncated" && d.Count == 10 && strings.Contains(d.Detail, "预算") {
			found = true
		}
	}
	if !found {
		t.Errorf("应说明截断数量与预算: %+v", res.Diagnostics)
	}
	// 稳定排序：再次查询结果一致
	res2, err := acts.AsyncLifecycle(AsyncLifecycleRequest{Seed: seed, RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Nodes) != len(res.Nodes) || res2.Nodes[0].ID != res.Nodes[0].ID ||
		res2.Nodes[len(res2.Nodes)-1].ID != res.Nodes[len(res.Nodes)-1].ID {
		t.Errorf("候选截断排序必须稳定")
	}
}

// canonical ID 不存在 → 明确错误（不静默变成表名搜索）。
func TestAsyncScopeSeedMustExist(t *testing.T) {
	dir, acts := seedAsyncScopeRepo(t, nil, nil, nil)
	_, err := acts.AsyncLifecycle(AsyncLifecycleRequest{
		Seed: "symbol:go:example.com/app:NoSuch", RepoAbs: dir})
	if err == nil {
		t.Fatal("不存在的 canonical seed 应报错")
	}
}

// seedAsyncScopeRepoOrigins 同 seedAsyncScopeRepo，另写 summary_origins。
func seedAsyncScopeRepoOrigins(t *testing.T, nodes []*domain.CodeEntity,
	fields []*domain.FunctionFieldSummary, facts []*domain.Fact,
	origins []*domain.SummaryOrigin) (string, *Actions) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats(nodes, facts, fields, origins); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "asyncscope-origins", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir, New(r)
}
