//go:build integration

package integration

// todo27 T02：命名函数列表的跨帧与到达定义（真实 init → CLI）。
//
// 覆盖：命名 slice 类型 / 方法值元素 / 列表别名 / 分支 append（公共前缀
// 确定 + 差异尾部互斥候选）/ 覆盖后只用新列表 / builtin append 不产生
// "未解析调用/回调逃逸"假诊断 / 空列表不执行。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t02Src = `package flow
type Fn func()
type List []Fn
func A(){HitA()}
func B(){HitB()}
type S struct{}
func(s *S)M(){HitS()}
func HitA(){}
func HitB(){}
func HitS(){}
func RunList(fs []Fn){for _,f:=range fs{f()}}
func RunOne(f Fn){f()}
func EntryNamedSlice(){l:=List{A,B};RunList(l)}
func EntryMethod(){s:=&S{};l:=[]Fn{s.M,A};RunList(l)}
func EntryMethodField(){RunOne((&S{}).M)}
func EntryAlias(){l:=[]Fn{A};m:=l;RunList(m)}
func EntryOverwrite(){l:=[]Fn{A};l=[]Fn{B};RunList(l)}
func EntrySibling(k bool){l:=[]Fn{A};if k{l=append(l,B)};RunList(l)}
func EntryEmpty(){var l []Fn;RunList(l)}
`

func t02Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t02Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// t02Walk 收集 (节点, 祖先是否 ambiguous) —— 候选后代的执行证明口径。
func t02Walk(n map[string]any, amb bool, out *[]struct {
	n   map[string]any
	amb bool
}) {
	if n == nil {
		return
	}
	*out = append(*out, struct {
		n   map[string]any
		amb bool
	}{n, amb})
	// 歧义口径：节点位于"候选分支"内（branch 且被标 ambiguous 或标签为
	// 互斥候选）。公共前缀虽在整体 ambiguous 的 range 节点下，但不在候选
	// 分支内——它仍是确定执行。
	next := amb
	if kind, _ := n["Kind"].(string); kind == "branch" {
		label, _ := n["Label"].(string)
		candBranch := strings.Contains(label, "候选")
		if candBranch {
			next = true
		}
	}
	for _, key := range []string{"Nodes", "Else"} {
		cs, _ := n[key].([]any)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok {
				t02Walk(m, next, out)
			}
		}
	}
}

// t02Hits 目标符号的调用节点及祖先歧义标记。
func t02Hits(t *testing.T, dir, entry, symbol string) (plain, ambiguous int) {
	t.Helper()
	root := querySeqJSON(t, dir, "symbol:go:example.com/flow:"+entry, "--depth", "12")
	var all []struct {
		n   map[string]any
		amb bool
	}
	t02Walk(root, false, &all)
	for _, it := range all {
		ev, _ := it.n["evidence"].(map[string]any)
		if ev == nil || ev["symbol_id"] != "symbol:go:example.com/flow:"+symbol {
			continue
		}
		if it.amb {
			ambiguous++
		} else {
			plain++
		}
	}
	return plain, ambiguous
}

// TestT02NamedListVariants：命名 slice / 方法值 / 别名 / 覆盖。
func TestT02NamedListVariants(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t02Fixture(t)
	for _, tc := range []struct{ entry, sym string }{
		{"EntryNamedSlice", "HitA"}, {"EntryNamedSlice", "HitB"},
		{"EntryMethod", "HitS"}, {"EntryMethod", "HitA"},
		{"EntryMethodField", "HitS"},
		{"EntryAlias", "HitA"},
	} {
		plain, _ := t02Hits(t, dir, tc.entry, tc.sym)
		if plain != 1 {
			t.Errorf("%s 应确定执行 %s 一次，got %d", tc.entry, tc.sym, plain)
		}
	}
	// 覆盖后只用新列表：B 执行，A 不执行。
	if plain, _ := t02Hits(t, dir, "EntryOverwrite", "HitB"); plain != 1 {
		t.Errorf("EntryOverwrite 应执行 HitB 一次，got %d", plain)
	}
	if plain, amb := t02Hits(t, dir, "EntryOverwrite", "HitA"); plain+amb != 0 {
		t.Errorf("覆盖后的旧列表不得执行 HitA（plain=%d amb=%d）", plain, amb)
	}
	// 空列表：零元素执行。
	if plain, amb := t02Hits(t, dir, "EntryEmpty", "HitA"); plain+amb != 0 {
		t.Errorf("空列表不应执行元素（plain=%d amb=%d）", plain, amb)
	}
}

// TestT02SiblingAppend：条件分支 append —— 公共前缀确定执行，差异尾部在
// 互斥候选分支内（带 ambiguous 祖先），不拼成确定顺序、不丢失前缀。
func TestT02SiblingAppend(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t02Fixture(t)
	if plain, amb := t02Hits(t, dir, "EntrySibling", "HitA"); plain != 1 || amb != 0 {
		t.Errorf("公共前缀 HitA 应确定执行一次（plain=%d amb=%d）", plain, amb)
	}
	if plain, amb := t02Hits(t, dir, "EntrySibling", "HitB"); plain != 0 || amb != 1 {
		t.Errorf("条件 append 的 HitB 应在一条歧义分支内（plain=%d amb=%d）", plain, amb)
	}
	// builtin append 不是未解析调用：不应出现"回调实参逃逸"假诊断。
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:EntrySibling",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	if strings.Contains(out, "未解析调用中逃逸") {
		t.Errorf("builtin append 不应产生\"回调逃逸\"诊断\n%s", out)
	}
}
