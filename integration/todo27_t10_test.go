//go:build integration

package integration

// todo27 T10：外部形参的词法回调与列表重绑定（真实 init → CLI + depth=1
// 不加载外部源码的身份检查）。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const t10TxSrc = `package tx
func Run(f func()){f()}
func RunNamed(process func()){process()}
func RunList(fs []func()){for _,f:=range fs{f()}}
func RunVariadic(fs ...func()){for _,f:=range fs{f()}}
func Make()func(){return func(){Inner()}}
func Inner(){}
func Unused(f func()){}
`

const t10FlowSrc = `package flow
import "example.com/tx"
func A(){HitA()}
func B(){HitB()}
func Local(){HitLocal()}
func HitA(){}
func HitB(){}
func HitLocal(){}
func Entry(){tx.Run(func(){HitA()})}
func EntrySameName(){process:=func(){HitLocal()};tx.RunNamed(process)}
func EntryList(){tx.RunList([]func(){A,B})}
func EntryVariadic(){tx.RunVariadic(A,B)}
func EntryClosure(){f:=tx.Make();f()}
func EntryUnused(){tx.Unused(func(){HitA()})}
`

func t10Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n\nrequire example.com/tx v0.0.0\nreplace example.com/tx => ./tx\n")
	writeFile(t, filepath.Join(dir, "tx", "go.mod"), "module example.com/tx\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "tx", "tx.go"), t10TxSrc)
	writeFile(t, filepath.Join(dir, "flow.go"), t10FlowSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func t10Seq(t *testing.T, dir, entry string, depth int) string {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:"+entry,
		"--repo", dir, "--code", "--depth", itoaInt(depth), "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	return out
}

// TestT10ExternalCallbacks：普通参数/同名形参/列表/ellipsis/返回闭包/未用。
func TestT10ExternalCallbacks(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t10Fixture(t)
	cases := []struct {
		entry, sym string
		want       int
	}{
		{"Entry", "flow:HitA", 1},
		{"EntrySameName", "flow:HitLocal", 1},
		{"EntrySameName", "flow:HitA", 0},
		{"EntryList", "flow:HitA", 1},
		{"EntryList", "flow:HitB", 1},
		{"EntryVariadic", "flow:HitA", 1},
		{"EntryVariadic", "flow:HitB", 1},
		{"EntryClosure", "tx:Inner", 1},
		{"EntryUnused", "flow:HitA", 0},
	}
	for _, c := range cases {
		out := t10Seq(t, dir, c.entry, 12)
		got := strings.Count(out, "id=symbol:go:example.com/"+c.sym+" verdict=")
		if got != c.want {
			t.Errorf("%s：%s 执行 %d 次（期望 %d）\n%s", c.entry, c.sym, got, c.want, out)
		}
	}
}

// TestT10DepthNoExternalLoad：depth=1 保留准确外部身份，不加载外部源码。
func TestT10DepthNoExternalLoad(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t10Fixture(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	root, err := acts.CodeSequence(action.CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 外部调用身份 = tx.Run（准确 canonical ID），深度诊断而非展开。
	var found bool
	var walk func(n *action.CodeSeqNode)
	walk = func(n *action.CodeSeqNode) {
		if n == nil {
			return
		}
		if n.Evidence != nil && n.Evidence.SymbolID == "symbol:go:example.com/tx:Run" {
			found = true
		}
		for _, c := range n.Nodes {
			walk(c)
		}
	}
	walk(root)
	if !found {
		t.Errorf("depth=1 应保留准确外部目标 tx.Run")
	}
	if root.Meta != nil && root.Meta.Timing != nil && root.Meta.Timing.ExternalLoads != 0 {
		t.Errorf("depth=1 不应加载外部源码（external_loads=%d）", root.Meta.Timing.ExternalLoads)
	}
}
