//go:build integration

package integration

// todo25 §7.3：跨仓同名接口方法隔离 + 冷暖对照（真实 init → CLI）。
//
// 两个仓库各有同名的 API.Insert/SaveService 与实现类型 Store；查询（含
// workspace 内的外层请求）必须只使用本仓库的实现候选，不得串到另一个仓库
// 或同接口的其它方法；同一输入重复查询（暖）结论与冷查询逐项一致。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25IfaceRepoMod = "module example.com/%s\n\ngo 1.26.2\n"

const todo25IfaceRepoSrc = `package api

type API interface {
	Insert() error
	SaveService() error
}

type Store struct{}

func (s *Store) Insert() error { HitInsert(); return nil }

func (s *Store) SaveService() error { HitSave(); return nil }

func HitInsert() {}

func HitSave() {}

func Run(a API) { _ = a.Insert() }

func Entry() { Run(&Store{}) }
`

// todo25IfaceRepoFixture 建两个独立仓库（同名接口/方法，不同模块路径），
// 各自真实 init，再建 workspace（外层请求的候选范围）。
func todo25IfaceRepoFixture(t *testing.T) (repoA, repoB, wsA, wsB string) {
	t.Helper()
	root := t.TempDir()
	repoA = filepath.Join(root, "repoa")
	repoB = filepath.Join(root, "repob")
	for _, r := range []struct{ dir, name string }{{repoA, "repoa"}, {repoB, "repob"}} {
		writeFile(t, filepath.Join(r.dir, "go.mod"), strings.Replace(todo25IfaceRepoMod, "%s", r.name, 1))
		writeFile(t, filepath.Join(r.dir, "api", "api.go"), todo25IfaceRepoSrc)
		gitRun(t, r.dir, "init")
		gitRun(t, r.dir, "add", "-A")
		gitRun(t, r.dir, "commit", "-m", "init")
	}
	for _, d := range []string{repoA, repoB} {
		clearIndex(t, d)
		if code := runCLI(t, "init", "--repo", d, "--workers", "1", "--progress", "none"); code != 0 {
			t.Fatalf("init %s exit=%d", d, code)
		}
	}
	ws := filepath.Join(root, "ws")
	if code := runCLI(t, "workspace", "init", "--dir", ws,
		"--repo", repoA, "--repo", repoB, "--build"); code != 0 {
		t.Fatalf("workspace init exit=%d", code)
	}
	return repoA, repoB, filepath.Join(ws, "repoa"), filepath.Join(ws, "repob")
}

// seqCallNodesByLabel 树上 Kind=call 且 Label 匹配的节点。
func seqCallNodesByLabel(root *action.CodeSeqNode, label string) []*action.CodeSeqNode {
	var out []*action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Kind == "call" && n.Label == label {
			out = append(out, n)
		}
	})
	return out
}

// seqControlFlow 调用链（不含项目/证据中的路径与耗时），用于冷暖逐项对照。
func seqControlFlow(root *action.CodeSeqNode) []string {
	var out []string
	walkSeq(root, func(n *action.CodeSeqNode) {
		line := n.Kind + "|" + n.Label + "|" + n.Actor
		if n.Evidence != nil {
			line += "|" + n.Evidence.SymbolID + "|" + n.Evidence.Reason
		}
		out = append(out, line)
	})
	return out
}

func TestTodo25WorkspaceIfaceRepoIsolation(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	repoA, repoB, wsA, wsB := todo25IfaceRepoFixture(t)
	for _, tc := range []struct {
		label   string
		dir     string
		entry   string
		own     string
		foreign string
	}{
		{"repoa-main", repoA, "symbol:go:example.com/repoa/api:Entry", "example.com/repoa/api:(Store).Insert", "example.com/repob/api"},
		{"repob-main", repoB, "symbol:go:example.com/repob/api:Entry", "example.com/repob/api:(Store).Insert", "example.com/repoa/api"},
		{"repoa-workspace", wsA, "symbol:go:example.com/repoa/api:Entry", "example.com/repoa/api:(Store).Insert", "example.com/repob/api"},
		{"repob-workspace", wsB, "symbol:go:example.com/repob/api:Entry", "example.com/repob/api:(Store).Insert", "example.com/repoa/api"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			entry := tc.entry
			cold := seqTreeJSON(t, tc.dir, entry, 10)
			if !hasSymbolWith(cold, tc.own) {
				t.Fatalf("%s 应具体化到本仓实现 %s:\n%+v", tc.label, tc.own, cold)
			}
			if got := seqCallNodesByLabel(cold, "HitInsert"); len(got) != 1 {
				t.Errorf("%s 应执行本仓 Insert 内部调用一次，got %d", tc.label, len(got))
			}
			if hasSymbolWith(cold, tc.foreign) {
				t.Errorf("%s 不得使用另一仓库的实现候选（%s）", tc.label, tc.foreign)
			}
			if got := seqCallNodesByLabel(cold, "HitSave"); len(got) != 0 {
				t.Errorf("%s 的 Insert 不得借用 SaveService 链路（HitSave %d 次）", tc.label, len(got))
			}
			warm := seqTreeJSON(t, tc.dir, entry, 10)
			if a, b := seqControlFlow(cold), seqControlFlow(warm); strings.Join(a, "\n") != strings.Join(b, "\n") {
				t.Errorf("%s 冷暖查询结论应一致：\ncold:\n%s\nwarm:\n%s", tc.label, strings.Join(a, "\n"), strings.Join(b, "\n"))
			}
		})
	}
}
