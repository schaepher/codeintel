package action

// todo12-1/7：effects 目标筛选（字段类型身份 / 返回槽）与路径预算。
//   - A1 字段目标：路径与汇总排除无关字段
//   - A2/A3 返回槽：名字与 #槽 归一化、不重编号
//   - A4 空值/不存在/歧义/越界/互斥 → 明确错误
//   - A5 不同类型同名字段不混用；无写入目标不误报非法
//   - A6 被调函数写目标：保留调用 + 实际写点 + 被调文件位置
//   - A7/A8 未知类型保守保留 partial；场景可达性不被目标筛选改变
//   - A9 不指定目标：既有 paths/汇总保持

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedEffectsRepo 写源码 + 最小索引（结构体字段元数据、函数节点、可选摘要/调用边）。
func seedEffectsRepo(t *testing.T, src string, nodes []*domain.CodeEntity,
	fields []*domain.FunctionFieldSummary, facts []*domain.Fact) (string, *Actions) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/effects\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
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
	if err := r.Save(&domain.BuildMeta{BuildID: "fx-effects", ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir, New(r)
}

const effectsFlowSrc = `package example

type Record struct { ApprovalState, AuditState string }

func Advance(record *Record, approved bool) (approval string, audit string) {
	record.AuditState = "logged"
	if approved {
		record.ApprovalState = "accepted"
		return record.ApprovalState, record.AuditState
	}
	record.ApprovalState = "pending"
	return record.ApprovalState, record.AuditState
}
`

func effectsBaseNodes() []*domain.CodeEntity {
	return []*domain.CodeEntity{
		{ID: "symbol:go:example.com/effects:Record", Kind: domain.KindStruct, Name: "Record",
			FilePath: "flow.go", LineStart: 3,
			Properties: map[string]any{"fields": []map[string]any{
				{"name": "ApprovalState"}, {"name": "AuditState"}}}},
		{ID: "symbol:go:example.com/effects:Advance", Kind: domain.KindFunction, Name: "Advance",
			FilePath: "flow.go", LineStart: 5},
	}
}

func effectsBaseFields() []*domain.FunctionFieldSummary {
	return []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/effects:Advance", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/effects.Record.AuditState", LineStart: 6},
		{FunctionID: "symbol:go:example.com/effects:Advance", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/effects.Record.ApprovalState", LineStart: 8},
	}
}

func mustEffects(t *testing.T, acts *Actions, req EffectsRequest) *EffectsResult {
	t.Helper()
	res, err := acts.Effects(req)
	if err != nil {
		t.Fatalf("Effects: %v", err)
	}
	return res
}

// A1：字段目标——路径只留 ApprovalState（AuditState 被排除），汇总同步。
func TestEffectsTargetFieldFilters(t *testing.T) {
	dir, acts := seedEffectsRepo(t, effectsFlowSrc, effectsBaseNodes(), effectsBaseFields(), nil)
	res := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Advance",
		RepoAbs: dir, Depth: 1, TargetField: "example.com/effects.Record.ApprovalState"})
	if res.TargetInfo == nil || res.TargetInfo.Kind != "field" ||
		res.TargetInfo.Field != "example.com/effects.Record.ApprovalState" {
		t.Fatalf("target = %+v", res.TargetInfo)
	}
	if res.Analysis == nil || res.Analysis.Status != "complete" || res.Analysis.Depth != 1 {
		t.Fatalf("analysis = %+v", res.Analysis)
	}
	if len(res.Paths) != 2 {
		t.Fatalf("应保留两条路径（筛选不改可达性）: %+v", res.Paths)
	}
	want := map[string]int{"p1": 8, "p2": 11}
	for _, p := range res.Paths {
		if !p.Reachable || len(p.Conditions) == 0 {
			t.Errorf("路径可达性/条件不得被目标筛选改变: %+v", p)
		}
		if len(p.Writes) != 1 {
			t.Fatalf("%s 应只保留 1 条目标写入，实为 %+v", p.ID, p.Writes)
		}
		w := p.Writes[0]
		if w.Field != "record.ApprovalState" || w.Line != want[p.ID] {
			t.Errorf("%s 写入 = %+v; want record.ApprovalState L%d", p.ID, w, want[p.ID])
		}
		if strings.Contains(w.Field, "AuditState") {
			t.Errorf("%s 不得含 AuditState: %+v", p.ID, w)
		}
		if p.Result == nil || len(p.Result.Slots) != 1 || p.Result.Slots[0] != "#0" ||
			len(p.Result.Returns) != 1 || p.Result.Returns[0] != "record.ApprovalState" {
			t.Errorf("%s result 应为 #0 → record.ApprovalState: %+v", p.ID, p.Result)
		}
	}
	// 函数级汇总同步筛选
	if len(res.DBWrites) != 1 || res.DBWrites[0].Field != "example.com/effects.Record.ApprovalState" {
		t.Fatalf("函数级 db_writes 应只含目标字段: %+v", res.DBWrites)
	}
	for _, r := range res.Result.Returns {
		if strings.Contains(r, "AuditState") {
			t.Errorf("函数级 result 不得含 AuditState: %+v", res.Result)
		}
	}
}

// A2/A3：approval 与 #0、audit 与 #1 归一化等价；不重编号。
func TestEffectsTargetReturnSlots(t *testing.T) {
	dir, acts := seedEffectsRepo(t, effectsFlowSrc, effectsBaseNodes(), effectsBaseFields(), nil)
	run := func(target string) *EffectsResult {
		return mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Advance",
			RepoAbs: dir, Depth: 1, TargetReturn: target})
	}
	byName0, bySlot0 := run("approval"), run("#0")
	if byName0.TargetInfo == nil || byName0.TargetInfo.Slot != "#0" || byName0.TargetInfo.Name != "approval" {
		t.Fatalf("approval target = %+v", byName0.TargetInfo)
	}
	a, _ := json.Marshal(byName0.Paths)
	b, _ := json.Marshal(bySlot0.Paths)
	if string(a) != string(b) {
		t.Fatalf("approval 与 #0 路径应等价:\n%s\n%s", a, b)
	}
	if len(byName0.Paths) != 2 {
		t.Fatalf("应保留两条路径: %+v", byName0.Paths)
	}
	for _, p := range byName0.Paths {
		if p.Result == nil || len(p.Result.Slots) != 1 || p.Result.Slots[0] != "#0" {
			t.Errorf("槽身份应为 #0: %+v", p.Result)
		}
		if len(p.Writes) != 1 || p.Writes[0].Field != "record.ApprovalState" {
			t.Errorf("approval 槽只依赖 ApprovalState 写入: %+v", p.Writes)
		}
	}

	byName1, bySlot1 := run("audit"), run("#1")
	if byName1.TargetInfo == nil || byName1.TargetInfo.Slot != "#1" || byName1.TargetInfo.Name != "audit" {
		t.Fatalf("audit target = %+v", byName1.TargetInfo)
	}
	c, _ := json.Marshal(byName1.Paths)
	d, _ := json.Marshal(bySlot1.Paths)
	if string(c) != string(d) {
		t.Fatalf("audit 与 #1 路径应等价:\n%s\n%s", c, d)
	}
	for _, p := range byName1.Paths {
		if p.Result == nil || len(p.Result.Slots) != 1 || p.Result.Slots[0] != "#1" {
			t.Errorf("槽不得重编号为 #0: %+v", p.Result)
		}
		if len(p.Result.Returns) != 1 || p.Result.Returns[0] != "record.AuditState" {
			t.Errorf("audit 槽返回值 = %+v", p.Result)
		}
		if len(p.Writes) != 1 || p.Writes[0].Field != "record.AuditState" || p.Writes[0].Line != 6 {
			t.Errorf("audit 槽只依赖 AuditState 写入（L6）: %+v", p.Writes)
		}
	}
}

// A4：空值/不存在/歧义/越界/互斥都明确报错。
func TestEffectsTargetErrors(t *testing.T) {
	dir, acts := seedEffectsRepo(t, effectsFlowSrc, append(effectsBaseNodes(),
		&domain.CodeEntity{ID: "symbol:go:example.com/other:Record", Kind: domain.KindStruct,
			Name: "Record", FilePath: "other.go", LineStart: 1,
			Properties: map[string]any{"fields": []map[string]any{{"name": "ApprovalState"}}}},
	), effectsBaseFields(), nil)
	cases := []struct {
		name string
		req  EffectsRequest
		want string
	}{
		{"空字段", EffectsRequest{TargetField: " ", TargetSet: true}, "目标为空"},
		{"空返回槽", EffectsRequest{TargetReturn: "", TargetSet: true}, "目标为空"},
		{"互斥", EffectsRequest{TargetField: "example.com/effects.Record.ApprovalState", TargetReturn: "audit"}, "互斥"},
		{"字段不存在", EffectsRequest{TargetField: "example.com/effects.Record.Nope"}, "不存在"},
		{"类型不存在", EffectsRequest{TargetField: "example.com/effects.NoSuch.Field"}, "不存在"},
		{"短名歧义", EffectsRequest{TargetField: "Record.ApprovalState"}, "匹配"},
		{"返回槽越界", EffectsRequest{TargetReturn: "#2"}, "越界"},
		{"返回槽不存在", EffectsRequest{TargetReturn: "NoSuchSlot"}, "不存在"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.req.Target = "symbol:go:example.com/effects:Advance"
			c.req.RepoAbs = dir
			_, err := acts.Effects(c.req)
			if err == nil {
				t.Fatalf("应报错（%s）", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("错误 = %q; 应包含 %q", err.Error(), c.want)
			}
		})
	}
}

// A5：不同类型同名字段不混用；合法无写入目标不误报非法。
func TestEffectsTargetOtherTypeNoWrite(t *testing.T) {
	nodes := append(effectsBaseNodes(), &domain.CodeEntity{
		ID: "symbol:go:example.com/effects:Other", Kind: domain.KindStruct, Name: "Other",
		FilePath: "flow.go", LineStart: 3,
		Properties: map[string]any{"fields": []map[string]any{{"name": "ApprovalState"}}}})
	dir, acts := seedEffectsRepo(t, effectsFlowSrc, nodes, effectsBaseFields(), nil)
	res := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Advance",
		RepoAbs: dir, Depth: 1, TargetField: "example.com/effects.Other.ApprovalState"})
	if res.TargetInfo == nil || res.TargetInfo.Field != "example.com/effects.Other.ApprovalState" {
		t.Fatalf("合法目标应被接受: %+v", res.TargetInfo)
	}
	if res.Analysis == nil || res.Analysis.Status != "complete" {
		t.Errorf("合法但无写入不算非法（complete）: %+v", res.Analysis)
	}
	if len(res.DBWrites) != 0 {
		t.Errorf("不同类型同名字段不得混入: %+v", res.DBWrites)
	}
	for _, p := range res.Paths {
		if len(p.Writes) != 0 {
			t.Errorf("%s 不得保留其他类型的 ApprovalState 写入: %+v", p.ID, p.Writes)
		}
		if p.Result != nil {
			t.Errorf("%s 返回不依赖目标字段，应省略 result: %+v", p.ID, p.Result)
		}
	}
	found := false
	for _, d := range res.Analysis.Diagnostics {
		if d.Code == EffectDiagTargetNoWrite {
			found = true
			if strings.Contains(d.Code, "partial") {
				t.Errorf("无写入诊断不应表示 partial: %+v", d)
			}
		}
	}
	if !found {
		t.Errorf("应说明目标没有写入: %+v", res.Analysis.Diagnostics)
	}
}

const effectsCalleeSrc = `package example

type Record struct { ApprovalState, AuditState string }

func Apply(r *Record) {
	r.ApprovalState = "applied"
}

func Advance(record *Record) error {
	Apply(record)
	return nil
}
`

// A6：别名传入被调函数写目标——保留调用、实际写点与被调文件位置。
func TestEffectsTargetCalleeWrite(t *testing.T) {
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/effects:Record", Kind: domain.KindStruct, Name: "Record",
			FilePath: "flow.go", LineStart: 3,
			Properties: map[string]any{"fields": []map[string]any{
				{"name": "ApprovalState"}, {"name": "AuditState"}}}},
		{ID: "symbol:go:example.com/effects:Apply", Kind: domain.KindFunction, Name: "Apply",
			FilePath: "flow.go", LineStart: 5},
		{ID: "symbol:go:example.com/effects:Advance", Kind: domain.KindFunction, Name: "Advance",
			FilePath: "flow.go", LineStart: 9},
	}
	fields := []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/effects:Apply", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/effects.Record.ApprovalState", LineStart: 6},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/effects:Advance", TargetID: "symbol:go:example.com/effects:Apply",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 10}},
	}
	dir, acts := seedEffectsRepo(t, effectsCalleeSrc, nodes, fields, facts)
	res := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Advance",
		RepoAbs: dir, Depth: 2, TargetField: "example.com/effects.Record.ApprovalState"})
	if len(res.Paths) != 1 {
		t.Fatalf("应一条路径: %+v", res.Paths)
	}
	p := res.Paths[0]
	if len(p.Writes) != 1 {
		t.Fatalf("路径应含被调写入: %+v", p.Writes)
	}
	w := p.Writes[0]
	if w.Function != "symbol:go:example.com/effects:Apply" || w.Line != 6 || w.File != "flow.go" {
		t.Errorf("被调写入位置应为 Apply flow.go:6，实为 %+v", w)
	}
	if w.Evidence == nil || w.Evidence.CallLine != 10 {
		t.Errorf("证据应带调用点行号: %+v", w.Evidence)
	}
	if len(p.Calls) != 1 || p.Calls[0].Function != "symbol:go:example.com/effects:Apply" ||
		!p.Calls[0].Target || p.Calls[0].Line != 10 {
		t.Errorf("应保留命中目标的调用: %+v", p.Calls)
	}
}

const effectsUnknownSrc = `package example

type Record struct { ApprovalState string }

func get() *Record { return &Record{} }

func Advance() {
	r := get()
	r.ApprovalState = "x"
}
`

// A7：类型未知的写入保守保留并报 partial（不静默丢弃）。
func TestEffectsTargetUnknownTypePartial(t *testing.T) {
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/effects:Record", Kind: domain.KindStruct, Name: "Record",
			FilePath: "flow.go", LineStart: 3,
			Properties: map[string]any{"fields": []map[string]any{{"name": "ApprovalState"}}}},
		{ID: "symbol:go:example.com/effects:get", Kind: domain.KindFunction, Name: "get",
			FilePath: "flow.go", LineStart: 5},
		{ID: "symbol:go:example.com/effects:Advance", Kind: domain.KindFunction, Name: "Advance",
			FilePath: "flow.go", LineStart: 7},
	}
	dir, acts := seedEffectsRepo(t, effectsUnknownSrc, nodes, nil, nil)
	res := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Advance",
		RepoAbs: dir, Depth: 1, TargetField: "example.com/effects.Record.ApprovalState"})
	if res.Analysis == nil || res.Analysis.Status != "partial" {
		t.Fatalf("未知类型应 partial: %+v", res.Analysis)
	}
	found := false
	for _, d := range res.Analysis.Diagnostics {
		if d.Code == EffectDiagTargetUnknown {
			found = true
		}
	}
	if !found {
		t.Errorf("应给出 target_dependency_unresolved 诊断: %+v", res.Analysis.Diagnostics)
	}
	if len(res.Paths) != 1 || len(res.Paths[0].Writes) != 1 {
		t.Fatalf("未知类型写入应保守保留: %+v", res.Paths)
	}
	ev := res.Paths[0].Writes[0].Evidence
	if ev == nil || !strings.Contains(ev.Reason, "unresolved-type") || ev.Verdict != SeqVerdictUnverified {
		t.Errorf("未知写入证据应标 unverified/unresolved-type: %+v", ev)
	}
}

// A8：场景排除不因目标筛选被丢弃；目标筛选不带回无关写入。
func TestEffectsTargetSceneReachability(t *testing.T) {
	src := `package example

type Record struct { ApprovalState, AuditState string }

func Route(region string, r *Record) {
	if region == "CN" {
		r.ApprovalState = "cn"
		r.AuditState = "audit-cn"
	} else {
		r.ApprovalState = "us"
	}
}
`
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/effects:Record", Kind: domain.KindStruct, Name: "Record",
			FilePath: "flow.go", LineStart: 3,
			Properties: map[string]any{"fields": []map[string]any{
				{"name": "ApprovalState"}, {"name": "AuditState"}}}},
		{ID: "symbol:go:example.com/effects:Route", Kind: domain.KindFunction, Name: "Route",
			FilePath: "flow.go", LineStart: 5},
	}
	dir, acts := seedEffectsRepo(t, src, nodes, nil, nil)
	res := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Route",
		RepoAbs: dir, Depth: 1, Scene: NewScene([]string{"region=CN"}),
		TargetField: "example.com/effects.Record.ApprovalState"})
	if len(res.Paths) != 2 {
		t.Fatalf("两条分支路径都应保留: %+v", res.Paths)
	}
	var unreachable int
	for _, p := range res.Paths {
		if !p.Reachable {
			unreachable++
		}
		for _, w := range p.Writes {
			if strings.Contains(w.Field, "AuditState") {
				t.Errorf("目标筛选不得带回无关写入: %+v", w)
			}
		}
	}
	if unreachable != 1 {
		t.Errorf("场景应排除 else 路径（reachable=false）: %+v", res.Paths)
	}
}

const effectsLongSrcPrefix = `package example

func Long(flags [30]bool) (int, bool) {
`

// A9/任务 7：默认预算不足 → partial + 预算诊断；提高预算覆盖末尾路径。
func TestEffectsPathBudget(t *testing.T) {
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
	if len(low.Paths) != maxEffectPaths {
		t.Fatalf("默认预算应保留 %d 条路径，实为 %d", maxEffectPaths, len(low.Paths))
	}
	if low.Analysis == nil || low.Analysis.Status != "partial" {
		t.Fatalf("预算截断必须 partial: %+v", low.Analysis)
	}
	var budgetDiag *SeqDiagCount
	for i := range low.Analysis.Diagnostics {
		if low.Analysis.Diagnostics[i].Code == EffectDiagPathBudget {
			budgetDiag = &low.Analysis.Diagnostics[i]
		}
	}
	if budgetDiag == nil || budgetDiag.Count == 0 || !strings.Contains(budgetDiag.Detail, "path-budget") {
		t.Fatalf("预算诊断应说明预算与遗漏: %+v", low.Analysis.Diagnostics)
	}
	for _, p := range low.Paths {
		if p.Result != nil && strings.Contains(strings.Join(p.Result.Returns, ","), "30") {
			t.Errorf("默认预算下不应已覆盖末尾 return（若覆盖则诊断不可信）")
		}
	}

	high := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Long",
		RepoAbs: dir, Depth: 1, PathBudget: 64})
	if len(high.Paths) != 31 {
		t.Fatalf("提高预算应有 31 条终态，实为 %d", len(high.Paths))
	}
	if high.Analysis == nil || high.Analysis.Status != "complete" {
		t.Fatalf("足够预算应 complete: %+v", high.Analysis)
	}
	last := high.Paths[len(high.Paths)-1]
	if last.Result == nil || strings.Join(last.Result.Returns, ",") != "30,true" {
		t.Errorf("末尾成功路径应包含 returns=[30 true]: %+v", last.Result)
	}
}

// A9：不指定目标时既有契约保持（paths/汇总不变）。
func TestEffectsNoTargetKeepsContract(t *testing.T) {
	dir, acts := seedEffectsRepo(t, effectsFlowSrc, effectsBaseNodes(), effectsBaseFields(), nil)
	res := mustEffects(t, acts, EffectsRequest{Target: "symbol:go:example.com/effects:Advance",
		RepoAbs: dir, Depth: 1})
	if res.TargetInfo != nil {
		t.Errorf("不指定目标不应输出 target_info: %+v", res.TargetInfo)
	}
	if len(res.Paths) != 2 {
		t.Fatalf("paths 数量 = %d; want 2", len(res.Paths))
	}
	for _, p := range res.Paths {
		if len(p.Writes) != 2 {
			t.Errorf("%s 应保留全部写入（无目标筛选）: %+v", p.ID, p.Writes)
		}
		if p.Result == nil || len(p.Result.Returns) != 2 || len(p.Result.Slots) != 2 {
			t.Errorf("%s 返回表达式/槽应完整: %+v", p.ID, p.Result)
		}
	}
	if len(res.DBWrites) != 2 {
		t.Errorf("函数级汇总应保留两个字段: %+v", res.DBWrites)
	}
}
