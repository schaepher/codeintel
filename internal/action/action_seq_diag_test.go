package action

// todo10-3：展开终点诊断 + 节点证据。
//   - 区分"分析停止"（depth/stop_pkg/cycle/unresolved/index_gap/
//     source_missing/func_not_found/parse_error）与"业务流程结束"（no_calls）
//   - 节点证据可定位源码（canonical ID / 文件 / 行 / 依据 / 置信度）
//   - 接口多候选不静默选一个（候选集合 + ambiguous）

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seqDiagFixture 建一个可分支到多种终点的仓库：
// main → leaf（无内部调用）/ recur（互相递归）/ missing（索引缺符号）/
// gone（索引有节点但源码文件不存在）/ callOnly（只有外部库调用）。
func seqDiagFixture(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

func leaf() {}

func recur() { other() }

func other() { recur() }

func main() {
	leaf()
	recur()
	missing()
	gone()
	strings.Title("x")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:leaf", Kind: domain.KindFunction, Name: "leaf", FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:recur", Kind: domain.KindFunction, Name: "recur", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:other", Kind: domain.KindFunction, Name: "other", FilePath: "main.go", LineStart: 7},
		// missing：节点存在但没有 file_path（虚拟/生成符号 → 构建覆盖缺失）
		{ID: "symbol:go:example.com/m:missing", Kind: domain.KindFunction, Name: "missing"},
		// gone：节点存在但 file_path 指向不存在的文件
		{ID: "symbol:go:example.com/m:gone", Kind: domain.KindFunction, Name: "gone", FilePath: "gone.go", LineStart: 1},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m:leaf",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(10)}},
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m:recur",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(11)}},
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m:missing",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(12)}},
		{SourceID: "symbol:go:example.com/m:main", TargetID: "symbol:go:example.com/m:gone",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(13)}},
		{SourceID: "symbol:go:example.com/m:recur", TargetID: "symbol:go:example.com/m:other",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(5)}},
		{SourceID: "symbol:go:example.com/m:other", TargetID: "symbol:go:example.com/m:recur",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(7)}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestSeqStopCodes 各类终点能被区分（分析停止 vs 业务流程结束）。
func TestSeqStopCodes(t *testing.T) {
	acts, dir := seqDiagFixture(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:main", RepoAbs: dir, Depth: 4})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	stops := map[string]string{} // 调用名 → stop code
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n.Stop != nil {
				stops[n.Label] = n.Stop.Code
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)

	want := map[string]string{
		"leaf":    SeqStopNoCalls,    // 业务流程结束（叶子函数无内部调用）
		"recur":   SeqStopCycle,      // 防环
		"missing": SeqStopIndexGap,   // 索引缺符号
		"gone":    SeqStopSourceMiss, // 索引有节点但源码缺失
	}
	for call, code := range want {
		if stops[call] != code {
			t.Errorf("%s 终点 = %q; want %q（全部=%v）", call, stops[call], code, stops)
		}
	}

	// meta.diagnostics 按 code 聚合（分析停止可从输出里看到，不只 JSON 树）
	if root.Meta == nil || len(root.Meta.Diagnostics) == 0 {
		t.Fatalf("应聚合展开终点诊断: %+v", root.Meta)
	}
	codes := map[string]int{}
	for _, d := range root.Meta.Diagnostics {
		codes[d.Code] = d.Count
	}
	if codes[SeqStopIndexGap] == 0 || codes[SeqStopSourceMiss] == 0 {
		t.Errorf("诊断聚合应含 index_gap/source_missing: %v", codes)
	}
}

// TestSeqStopDepthAndPkg：深度上限与配置停止包都记为分析停止。
func TestSeqStopDepthAndPkg(t *testing.T) {
	acts, dir := seqDiagFixture(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:main", RepoAbs: dir, Depth: 1})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	depthStops := 0
	for _, n := range root.Nodes {
		if n.Stop != nil && n.Stop.Code == SeqStopDepthLimit {
			depthStops++
		}
	}
	if depthStops == 0 {
		t.Errorf("depth 1 应给可展开调用点标 %q: %+v", SeqStopDepthLimit, root.Nodes)
	}

	root2, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "example.com/m:main", RepoAbs: dir, Depth: 4,
		StopPackages: []string{"example.com/m"}, // 命中 leaf 所在包 → 不深入
	})
	if err != nil || root2 == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root2)
	}
	pkgStops := 0
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n.Stop != nil && n.Stop.Code == SeqStopPkg {
				pkgStops++
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root2.Nodes)
	if pkgStops == 0 {
		t.Errorf("停止包命中应标 %q: %+v", SeqStopPkg, root2.Nodes)
	}
}

// TestSeqDiagErrorActionable：根节点无法建立时返回带 code 的错误（可操作）。
func TestSeqDiagErrorActionable(t *testing.T) {
	acts, dir := seqDiagFixture(t)
	// 符号在索引里但源码文件不存在 → source_missing
	_, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:gone", RepoAbs: dir, Depth: 1})
	var diag *SeqDiagError
	if !errors.As(err, &diag) {
		t.Fatalf("应返回 SeqDiagError，实为 %v", err)
	}
	if diag.Code != SeqStopSourceMiss {
		t.Errorf("code = %q; want %q", diag.Code, SeqStopSourceMiss)
	}
	if !strings.Contains(diag.Error(), "update") {
		t.Errorf("错误信息应给出可操作提示: %s", diag.Error())
	}
}

// TestSeqEvidenceLocatable：节点证据能定位源码（ID/文件/行/依据/置信度）。
func TestSeqEvidenceLocatable(t *testing.T) {
	acts, dir := seqDiagFixture(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:main", RepoAbs: dir, Depth: 4})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	if root.Evidence == nil || root.Evidence.Source != SeqEvidenceAST ||
		root.Evidence.SymbolID != "symbol:go:example.com/m:main" ||
		root.Evidence.File != "main.go" || root.Evidence.Line != 9 {
		t.Fatalf("根节点应有 AST 证据（ID/文件/行）: %+v", root.Evidence)
	}
	var leaf *CodeSeqNode
	for _, n := range root.Nodes {
		if n.Label == "leaf" {
			leaf = n
		}
	}
	if leaf == nil || leaf.Evidence == nil {
		t.Fatalf("调用节点应有证据: %+v", leaf)
	}
	ev := leaf.Evidence
	if ev.Source != SeqEvidenceCallEdge || ev.CallFile != "main.go" || ev.CallLine != 10 {
		t.Errorf("调用点证据应含 call_file/call_line: %+v", ev)
	}
	if ev.SymbolID != "symbol:go:example.com/m:leaf" {
		t.Errorf("证据应含被调符号 canonical ID: %+v", ev)
	}
	if ev.Confidence <= 0 {
		t.Errorf("应复用 calls 边置信度: %+v", ev)
	}
}
