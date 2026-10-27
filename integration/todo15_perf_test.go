//go:build integration

package integration

// todo15-17 A1/A2/A3：固定大仓矩阵语义/诊断对照、冷暖/并发、目标查询峰值
// 采样（/usr/bin/time -v）与进入工作后的取消证据。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func buildTodo15Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package large\n\n")
	b.WriteString("type Doer interface{ Do() }\n")
	b.WriteString("type Impl struct{}\n")
	b.WriteString("func (Impl) Do() {}\n")
	b.WriteString("func Use(d Doer) { d.Do() }\n")
	b.WriteString("func NoImpl(d Doer) { d.Do() }\n")
	b.WriteString("func CB(f func() string) string { return f() }\n")
	b.WriteString("type H struct{ S string }\n")
	b.WriteString("func (h *H) Unknown(f func() string) string { h.S = f(); return h.S }\n")
	b.WriteString("func (h *H) Simple() { h.S = \"x\" }\n")
	b.WriteString("func (h *H) Many() {\n\th.S = \"a\"\n")
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&b, "\t_ = h.S\n\th.S = \"b%d\"\n", i)
	}
	b.WriteString("}\n\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "type U%d struct{ Cache string }\n", i)
		fmt.Fprintf(&b, "func (u *U%d) Store() { u.Cache = \"x\" }\n\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/matrix15\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func seqJSONT(t *testing.T, dir, sym string, depth int) map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", sym, "--repo", dir, "--code",
		"--depth", strconv.Itoa(depth), "--json")
	if code != 0 {
		t.Fatalf("sequence %s exit=%d; %s", sym, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("sequence JSON: %v", err)
	}
	return root
}

func seqHasCode(root map[string]any, code string) bool {
	found := false
	walkSeqJSON(root, func(n map[string]any) {
		if stop, _ := n["stop"].(map[string]any); stop != nil {
			if c, _ := stop["code"].(string); c == code {
				found = true
			}
		}
	})
	return found
}

func countJSONNodes(root map[string]any) int {
	n := 0
	walkSeqJSON(root, func(map[string]any) { n++ })
	return n
}

func TestTodo15MatrixSemantics(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := buildTodo15Fixture(t)
	use := seqJSONT(t, dir, "symbol:go:example.com/matrix15:Use", 3)
	if !strings.Contains(fmt.Sprint(use), "Impl") {
		t.Errorf("接口派发应展开到 Impl.Do: %+v", use)
	}
	// 同一接口有 Impl 实现时，NoImpl 应按候选具体化到 Impl.Do（接口派发覆盖）
	no := seqJSONT(t, dir, "symbol:go:example.com/matrix15:NoImpl", 3)
	if !strings.Contains(fmt.Sprint(no), "Impl") {
		t.Errorf("接口调用应按候选具体化（Impl.Do）: %+v", no)
	}
	// todo22 §2（2026-10-27）：函数值调用点从笼统 unresolved 改为更精确的
	// func_value_unbound（"有明确调用点但回调值无法确定"）——语义仍是
	// **未知、不展开**，只是诊断码细分了。CB 的形参 f 是入口参数、无实参
	// 绑定，所以这里必须保留未知节点而不是猜一个回调。
	cb := seqJSONT(t, dir, "symbol:go:example.com/matrix15:CB", 3)
	if !seqHasCode(cb, "func_value_unbound") {
		t.Errorf("未知回调调用点应 func_value_unbound（未知、不展开）: %+v", cb)
	}
	// 未知回调不得被具体化到任何实现（没有 ImplType / 具体符号 ID）。
	cbs := []map[string]any{}
	seqJSONNodes(cb, "f", &cbs)
	if len(cbs) != 1 {
		t.Errorf("未知回调应保留 1 个 f 调用节点，got %d", len(cbs))
	} else {
		if v, _ := cbs[0]["ImplType"].(string); v != "" {
			t.Errorf("未知回调不得具体化实现类型，got %q", v)
		}
		if nodes, _ := cbs[0]["Nodes"].([]any); len(nodes) != 0 {
			t.Errorf("未知回调不得展开内部执行，got %d 子节点", len(nodes))
		}
	}
	d1 := seqJSONT(t, dir, "symbol:go:example.com/matrix15:Use", 1)
	d2 := seqJSONT(t, dir, "symbol:go:example.com/matrix15:Use", 2)
	n1, n2 := countJSONNodes(d1), countJSONNodes(d2)
	t.Logf("matrix depth1 nodes=%d depth2 nodes=%d", n1, n2)
	if n1 == 0 || n2 < n1 {
		t.Errorf("depth 覆盖异常: d1=%d d2=%d", n1, n2)
	}
}

func TestTodo15ValueTraceMatrixAndConcurrency(t *testing.T) {
	dir := buildTodo15Fixture(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	repo := sqlite.NewRepo(db)
	unknownWrite := fieldAccessID(t, repo, "symbol:go:example.com/matrix15:(H).Unknown", "h.S", "write")
	simpleWrite := fieldAccessID(t, repo, "symbol:go:example.com/matrix15:(H).Simple", "h.S", "write")
	nodes, edges, _ := repo.Counts()
	db.Close()
	if unknownWrite == "" || simpleWrite == "" {
		t.Fatalf("未找到写节点: unknown=%q simple=%q", unknownWrite, simpleWrite)
	}
	t.Logf("fixture nodes=%d edges=%d", nodes, edges)
	openActs := func() *action.Actions {
		d, err := sqlite.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Close() })
		return action.New(sqlite.NewRepo(d))
	}
	acts := openActs()
	rows, st, err := acts.ValueTraceWithContext(t.Context(), domain.CanonicalID(unknownWrite), 8, 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	_, st2, err := acts.ValueTraceWithContext(t.Context(), domain.CanonicalID(unknownWrite), 8, 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	if st2.CacheHits == 0 {
		t.Errorf("暖查询应命中缓存: %+v", st2)
	}
	t.Logf("value-trace unknown: rows=%d sql=%v sym=%v cond=%v dispatch=%v; warm hit=%d miss=%d",
		len(rows), st.SQLQueries, st.SymbolLookups, st.ConditionRows, st.DispatchCalls,
		st2.CacheHits, st2.CacheMisses)
	simple, _, err := acts.ValueTraceWithContext(t.Context(), domain.CanonicalID(simpleWrite), 8, 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("value-trace simple rows=%d", len(simple))
	if len(simple) == 0 || len(rows) == 0 {
		t.Fatalf("value-trace 行数不应为 0")
	}
	var mu sync.Mutex
	var samples []time.Duration
	var rowCounts []int
	var wg sync.WaitGroup
	for round := 0; round < 2; round++ {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a := openActs()
				start := time.Now()
				rs, _, err := a.ValueTraceWithContext(t.Context(), domain.CanonicalID(unknownWrite), 8, 1.0, false)
				el := time.Since(start)
				if err != nil {
					t.Errorf("concurrent value-trace: %v", err)
					return
				}
				mu.Lock()
				samples = append(samples, el)
				rowCounts = append(rowCounts, len(rs))
				mu.Unlock()
			}()
		}
		wg.Wait()
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) == 0 {
		t.Fatal("并发样本为空")
	}
	for _, n := range rowCounts {
		if n != len(rows) {
			t.Errorf("并发结果行数应一致: got %d want %d", n, len(rows))
		}
	}
	t.Logf("concurrent value-trace n=%d p50=%s max=%s", len(samples),
		samples[len(samples)/2].Round(time.Microsecond), samples[len(samples)-1].Round(time.Microsecond))
}
