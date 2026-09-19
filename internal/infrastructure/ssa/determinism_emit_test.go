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
