package action

// todo13-13：外部目标/未知实现/索引缺失/正常叶子可区分。
//   - strings.TrimSpace → external_target（不笼统 index_gap）
//   - 接口 Runner.Run 无实现 → unknown_implementation（保留接口身份/调用位置）
//   - 正常叶子 → no_calls（不伪报索引缺失）
//   - 同 module 未索引导入 → index_gap

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const seqClassifySrc = `package m

import "strings"

type Runner interface{ Run() }

func Leaf() {}

func External() {
	_ = strings.TrimSpace(" x ")
}

func Unknown(r Runner) {
	r.Run()
}

func Caller(r Runner) {
	Leaf()
	External()
	Unknown(r)
}
`

func seedSeqClassifyRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(seqClassifySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Caller", Kind: domain.KindFunction, Name: "Caller", FilePath: "main.go", LineStart: 17},
		{ID: "symbol:go:example.com/m:Leaf", Kind: domain.KindFunction, Name: "Leaf", FilePath: "main.go", LineStart: 7},
		{ID: "symbol:go:example.com/m:External", Kind: domain.KindFunction, Name: "External", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:Unknown", Kind: domain.KindFunction, Name: "Unknown", FilePath: "main.go", LineStart: 13},
		{ID: "symbol:go:example.com/m:Runner", Kind: domain.KindInterface, Name: "Runner"},
		{ID: "symbol:go:example.com/m:(Runner).Run", Kind: domain.KindMethod, Name: "(Runner).Run"},
	}
	call := func(target string, line int) *domain.Fact {
		return &domain.Fact{SourceID: "symbol:go:example.com/m:Caller", TargetID: domain.CanonicalID(target),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(line)}}
	}
	facts := []*domain.Fact{
		call("symbol:go:example.com/m:Leaf", 18),
		call("symbol:go:example.com/m:External", 19),
		call("symbol:go:example.com/m:Unknown", 20),
		{SourceID: "symbol:go:example.com/m:Unknown", TargetID: "symbol:go:example.com/m:(Runner).Run",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(14)}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "classify", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func TestSeqCallClassification(t *testing.T) {
	acts, dir := seedSeqClassifyRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:Caller", RepoAbs: dir, Depth: 3})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	type info struct {
		stop   string
		detail string
		line   int
		hasEv  bool
	}
	got := map[string]info{}
	var walk func([]*CodeSeqNode)
	walk = func(ns []*CodeSeqNode) {
		for _, n := range ns {
			if n.Kind == "call" {
				in := info{line: n.Line, hasEv: n.Evidence != nil && n.Evidence.CallLine > 0}
				if n.Stop != nil {
					in.stop, in.detail = n.Stop.Code, n.Stop.Detail
				} else if len(n.Nodes) > 0 && n.Nodes[0].Stop != nil {
					// 展开子树的终点（叶子/外部目标）
					in.stop, in.detail = n.Nodes[0].Stop.Code, n.Nodes[0].Stop.Detail
				}
				got[n.Label] = in
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)

	if in := got["Leaf"]; in.stop != SeqStopNoCalls {
		t.Errorf("正常叶子应为 no_calls（不伪报索引缺失）: %+v", in)
	}
	if in := got["External"]; in.stop != SeqStopExternalTarget || !strings.Contains(in.detail, "strings") {
		t.Errorf("外部库目标应单列 external_target: %+v", in)
	}
	if in := got["r.Run"]; in.stop != SeqStopUnknownImpl {
		t.Errorf("接口无实现应为 unknown_implementation: %+v", in)
	} else if !in.hasEv || in.line == 0 {
		t.Errorf("未知实现应保留调用边证据与调用位置: %+v", in)
	}
	if in := got["External"]; strings.Contains(in.detail, "index_gap") {
		t.Errorf("外部目标不得笼统称 index_gap: %+v", in)
	}

	// 直接分类：同 module 未索引包 / 已索引包缺符号 → index_gap
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", `package m

import (
	"example.com/m/missingpkg"
	"example.com/m/sub"
)

func F() { missingpkg.Do(); sub.Do() }
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	indexed := map[string]bool{"example.com/m/sub": true}
	gotCodes := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			code, _ := classifyUnresolvedCall(f, call.Fun, "example.com/m", indexed)
			gotCodes[id.Name] = code
		}
		return true
	})
	if gotCodes["missingpkg"] != SeqStopIndexGap {
		t.Errorf("同 module 未索引包应为 index_gap: %+v", gotCodes)
	}
	if gotCodes["sub"] != SeqStopIndexGap {
		t.Errorf("已索引包但缺符号应为 index_gap: %+v", gotCodes)
	}
}
