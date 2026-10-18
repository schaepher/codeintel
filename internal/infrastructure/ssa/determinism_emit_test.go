package ssa

// Q250/Q252：发射端确定性（摘要代表选择）与包缓存保真度测试。
// 确定性夹具/快照工具在 determinism_test.go（同包共用）。

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q250：同一字段路径有多个候选条目时，赢家由内容决定（最早行号；
// 同行取 instance_path 字典序最小），与 entries 的构造顺序无关。
func TestEmitSummaryRowsOrderIndependent(t *testing.T) {
	a := fieldEntry{fieldPath: "m.T.X", instancePath: "t49.Items", line: 210, snippet: "late"}
	b := fieldEntry{fieldPath: "m.T.X", instancePath: "*query.Items", line: 17, snippet: "early"}
	c := fieldEntry{fieldPath: "m.T.Y", instancePath: "b", line: 17}
	collect := func(entries []fieldEntry) []string {
		var out []string
		err := emitSummaryRows("symbol:go:m:f", domain.SummaryDirectWrite, entries,
			func(item domain.Item) error {
				if item.Summary != nil {
					out = append(out, fmt.Sprintf("%s|%s|%d", item.Summary.FieldPath,
						item.Summary.InstancePath, item.Summary.LineStart))
				}
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(out)
		return out
	}
	want := []string{"m.T.X|*query.Items|17", "m.T.Y|b|17"}
	if got := collect([]fieldEntry{a, b, c}); !equalStrs(got, want) {
		t.Errorf("顺序 1 = %v, want %v", got, want)
	}
	if got := collect([]fieldEntry{c, b, a}); !equalStrs(got, want) {
		t.Errorf("顺序 2 = %v, want %v", got, want)
	}
	// 同行：instance_path 字典序最小者赢（'*' < 'a'）
	if got := collect([]fieldEntry{a, b, {fieldPath: "m.T.X", instancePath: "a.Z", line: 17}}); !equalStrs(got, []string{"m.T.X|*query.Items|17"}) {
		t.Errorf("同行应取 instance_path 最小，got %v", got)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Q252：**缓存重放保真度**——同一 fixture 连续构建两次（第二次全部命中
// 包缓存），产物必须一致。
//
// 为什么需要（实测 go2o）：暖构建 127/127 命中时边少 43、摘要少 1313
// （direct_write 少 1087），与序列化格式无关（JSON 缓存同样如此）——
// 根因是**缓存收集用覆盖语义**：`blkFD[owner] = fd`，而全局 a.fd 用
// `mergeFuncData` 的追加语义；闭包归到外层函数时同一 owner 会被 emitFunction
// 返回多次，缓存只留最后一份 → 缓存文件本身缺条目（埋点：冷 funcs=8922
// writes=6656 vs 暖 funcs=8899 writes=5556）。
func TestPkgCacheReplayFidelity(t *testing.T) {
	files := map[string]string{
		"go.mod":     determinismFixture,
		"types/t.go": determinismTypes,
		"impl/i.go":  determinismImpl,
		"main.go":    determinismMain,
	}
	cold := buildTwiceSnapshot(t, files, false)
	warm := buildTwiceSnapshot(t, files, true)
	if len(cold.summaries) == 0 || len(cold.edges) == 0 {
		t.Fatalf("fixture 未产出（nodes=%d edges=%d summaries=%d）",
			len(cold.nodes), len(cold.edges), len(cold.summaries))
	}
	for _, c := range []struct {
		name string
		a, b []string
	}{
		{"节点", cold.nodes, warm.nodes},
		{"边", cold.edges, warm.edges},
		{"摘要", cold.summaries, warm.summaries},
	} {
		if d := diffLines(c.a, c.b); len(d) > 0 {
			t.Errorf("%s 缓存重放后不一致（冷 %d / 暖 %d，差异 %d 条）：\n  %s",
				c.name, len(c.a), len(c.b), len(d), text(d, 0))
		}
	}
}

// buildTwiceSnapshot：在同一目录构建两次（第二次复用第一次写的包缓存），
// 返回第二次（=缓存重放）的产物快照；warmOnly=false 时返回第一次（冷）。
func buildTwiceSnapshot(t *testing.T, files map[string]string, warmOnly bool) *determinismSnapshot {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		writeFile(t, filepath.Join(dir, path), content)
	}
	var first, second *determinismSnapshot
	for i := 0; i < 2; i++ {
		snap := buildSnapshotAt(t, dir)
		if i == 0 {
			first = snap
		} else {
			second = snap
		}
	}
	if warmOnly {
		return second
	}
	return first
}

// TestEmitSummaryRowsSnippetTieBreak：行号 + instance_path 相同但片段不同
// 时，代表片段必须由内容（snippet 字典序）决定——条目来自 map（valuesOf
// 迭代顺序随机），平局不断定到底会"候选顺序决定代表片段"（R11）。
func TestEmitSummaryRowsSnippetTieBreak(t *testing.T) {
	e1 := fieldEntry{fieldPath: "m.T.X", instancePath: "o.X", line: 5, snippet: "zzz"}
	e2 := fieldEntry{fieldPath: "m.T.X", instancePath: "o.X", line: 5, snippet: "aaa"}
	pick := func(entries []fieldEntry) string {
		var got string
		err := emitSummaryRows("symbol:go:m:f", domain.SummaryIndirectWrite, entries,
			func(item domain.Item) error {
				if item.Summary != nil {
					got = item.Summary.CodeSnippet
				}
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if a, b := pick([]fieldEntry{e1, e2}), pick([]fieldEntry{e2, e1}); a != "aaa" || b != "aaa" {
		t.Errorf("代表片段应稳定取字典序最小 aaa，got %q / %q", a, b)
	}
}

// TestIndirectWriteCycleTerminates：环形调用（a→b→a）下间接写传播必须
// 终止且两函数都得到该字段的间接写摘要（队列 + 已见键去重，不死循环）。
func TestIndirectWriteCycleTerminates(t *testing.T) {
	_, _, summaries := indexFixtureFull(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Order struct {
	Fee float64
}

func a(o *Order) {
	o.Fee = 1
	b(o)
}

func b(o *Order) {
	a(o)
}

func use(o *Order) { a(o) }
`,
	})
	hasIndirect := func(funcID string) bool {
		for _, s := range summaries {
			if string(s.FunctionID) == funcID && s.AccessKind == domain.SummaryIndirectWrite &&
				s.FieldPath == "example.com/mtest.Order.Fee" {
				return true
			}
		}
		return false
	}
	if !hasIndirect("symbol:go:example.com/mtest:b") {
		t.Errorf("环形调用下 b 应得到 Order.Fee 间接写摘要")
	}
}

// TestIndirectWriteSameLineCallSites：同一行的两个调用点（实参不同）——
// 间接写传播按 (字段, 行号, 调用点 Pos) 区分，不按行号合并混用实参；
// INDIRECT_WRITE 边的调用点元数据跨多次构建稳定（todo2-8）。
func TestIndirectWriteSameLineCallSites(t *testing.T) {
	files := map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type V struct {
	F    func() bool
	Seen int
}

func fill(vs []V) { vs[0].Seen = 1 }

func call(xs []V, ys []V) {
	fill(xs); fill(ys)
}
`,
	}
	var first string
	for i := 0; i < 10; i++ {
		_, facts := indexFixture(t, files)
		var args string
		line := -1
		for _, f := range facts {
			if f.Kind != domain.FactIndirectWrite {
				continue
			}
			if v, ok := f.Metadata["call_args"].(string); ok {
				args = v
			}
			switch v := f.Metadata["call_line"].(type) {
			case int:
				line = v
			case float64:
				line = int(v)
			}
		}
		if line <= 0 {
			t.Fatalf("第 %d 次：INDIRECT_WRITE 边缺 call_line", i)
		}
		if args != "xs" && args != "ys" {
			t.Errorf("call_args = %q, want xs 或 ys（不得混用两调用点实参）", args)
		}
		if i == 0 {
			first = args
		} else if args != first {
			t.Errorf("call_args 跨构建抖动：%q vs %q", first, args)
		}
	}
}
