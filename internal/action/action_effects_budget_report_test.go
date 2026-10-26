package action

// todo13-7：路径预算诊断补预算值、停止位置与前沿口径。
//   - 默认查询：partial；已保留/上限=24/24；停止位置与未展开范围可核对
//   - 未展开前沿 ≠ 已证明的遗漏终态数；额外终态总数 unknown
//   - 高预算对照：complete、31 条终态、含 returns=[30 true]

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestEffectsPathBudgetReport(t *testing.T) {
	var b strings.Builder
	b.WriteString(effectsLongSrcPrefix)
	for i := 0; i < 30; i++ {
		b.WriteString("\tif flags[")
		b.WriteString(itoaSeq(i))
		b.WriteString("] { return ")
		b.WriteString(itoaSeq(i))
		b.WriteString(", false }\n")
	}
	b.WriteString("\treturn 30, true\n}\n")
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/effects:Long", Kind: domain.KindFunction, Name: "Long",
			FilePath: "flow.go", LineStart: 3},
	}
	dir, acts := seedEffectsRepo(t, b.String(), nodes, nil, nil)
	low := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Long",
		RepoAbs: dir, Depth: 1})
	if len(low.Paths) != 24 || low.Analysis == nil || low.Analysis.Status != "partial" {
		t.Fatalf("默认预算应 partial 且保留 24 条: paths=%d analysis=%+v", len(low.Paths), low.Analysis)
	}
	pb := low.Analysis.PathBudget
	if pb == nil {
		t.Fatalf("必须给出结构化预算口径: %+v", low.Analysis)
	}
	if pb.Used != 24 || pb.Limit != 24 {
		t.Errorf("已用/上限应为 24/24: %+v", pb)
	}
	if pb.StopFile != "flow.go" || pb.StopLine <= 0 {
		t.Errorf("停止位置应为实际文件:正行号: %+v", pb)
	}
	if strings.TrimSpace(pb.Scope) == "" {
		t.Errorf("应给出未展开的语句/分支范围: %+v", pb)
	}
	if pb.Frontier <= 0 {
		t.Errorf("未展开前沿应 >0: %+v", pb)
	}
	if pb.ExtraTerminals != "unknown" {
		t.Errorf("低预算下额外终态总数应为 unknown（不能以前沿数冒充）: %+v", pb)
	}
	var diag *SeqDiagCount
	for i := range low.Analysis.Diagnostics {
		if low.Analysis.Diagnostics[i].Code == EffectDiagPathBudget {
			diag = &low.Analysis.Diagnostics[i]
		}
	}
	if diag == nil {
		t.Fatal("应有路径预算诊断")
	}
	for _, want := range []string{"24/24", "停止位置=flow.go:", "未展开前沿=", "unknown"} {
		if !strings.Contains(diag.Detail, want) {
			t.Errorf("诊断应含 %q: %s", want, diag.Detail)
		}
	}
	if strings.Contains(diag.Detail, "丢弃 1 个未展开终态") {
		t.Errorf("不得把前沿口径说成遗漏终态数: %s", diag.Detail)
	}

	high := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Long",
		RepoAbs: dir, Depth: 1, PathBudget: 64})
	if len(high.Paths) != 31 || high.Analysis == nil || high.Analysis.Status != "complete" {
		t.Fatalf("高预算应 complete 且 31 条: paths=%d analysis=%+v", len(high.Paths), high.Analysis)
	}
	if high.Analysis.PathBudget != nil {
		t.Errorf("未截断时不应输出预算口径: %+v", high.Analysis.PathBudget)
	}
	last := high.Paths[len(high.Paths)-1]
	if last.Result == nil || strings.Join(last.Result.Returns, ",") != "30,true" {
		t.Errorf("末尾路径应含 returns=[30 true]: %+v", last.Result)
	}
	// 高低对照：额外终态 7 条（低预算不可知，高预算可数）
	if extra := len(high.Paths) - len(low.Paths); extra != 7 {
		t.Errorf("高低预算对照应差 7 条终态，实为 %d", extra)
	}
}
