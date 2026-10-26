//go:build integration

package integration

// todo12-3/4 验收：真实源码 → 生产索引 → CLI。
//   - Reassign 的实际值为 US → 内层条件 excluded；ConditionalReassign → unknown
//   - Dispatch taken / DispatchDynamic unknown 保持
//   - Pick --scene platform=US → ImplUS（记录取反依据）；无场景 ambiguous

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const todo12SceneFlow = `package regression

func Audit() {}

func Notify() {}

func Route(region string) {
	if region == "CN" {
		Audit()
	} else {
		Notify()
	}
}

func RouteFrom(region string) {
	Route(region)
}

func Dispatch() {
	region := "CN"
	RouteFrom(region)
}

func DispatchDynamic(region string) {
	RouteFrom(region)
}

func Reassign() {
	region := "CN"
	region = "US"
	RouteFrom(region)
}

func ConditionalReassign(change bool) {
	region := "CN"
	if change {
		region = "US"
	}
	RouteFrom(region)
}

type Doer interface { Do() }
type ImplCN struct{}
type ImplUS struct{}
func (ImplCN) Do() {}
func (ImplUS) Do() {}
func Pick(platform string) {
	var d Doer
	if platform == "CN" {
		d = &ImplCN{}
	} else {
		d = &ImplUS{}
	}
	d.Do()
}
`

func setupTodo12SceneRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenarios.go"), []byte(todo12SceneFlow), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// walkSeqJSON 遍历 sequence JSON 树（Kind/Label 大写键；Evidence/Scene 小写）。
func walkSeqJSON(n map[string]any, fn func(map[string]any)) {
	fn(n)
	for _, key := range []string{"Nodes", "nodes", "Else", "else"} {
		if ns, ok := n[key].([]any); ok {
			for _, x := range ns {
				if m, ok := x.(map[string]any); ok {
					walkSeqJSON(m, fn)
				}
			}
		}
	}
}

func querySeqJSON(t *testing.T, dir, sym string, extra ...string) map[string]any {
	t.Helper()
	args := append([]string{"query", "sequence", sym, "--repo", dir, "--code", "--json"}, extra...)
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("sequence exit = %d; %s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("sequence JSON: %v\n%s", err, out)
	}
	return root
}

func scenarioFit(t *testing.T, root map[string]any) map[string]any {
	t.Helper()
	var found map[string]any
	walkSeqJSON(root, func(n map[string]any) {
		if found != nil {
			return
		}
		if lbl, _ := n["Label"].(string); lbl == `region == "CN"` {
			if sc, ok := n["scene"].(map[string]any); ok {
				found = sc
			}
		}
	})
	if found == nil {
		t.Fatalf("未找到内层条件场景判定: %+v", root)
	}
	return found
}

func TestTodo12SceneReachingRealIndex(t *testing.T) {
	dir := setupTodo12SceneRepo(t)
	base := "symbol:go:example.com/codeintel-regression:"

	// Reassign：实际值 US → excluded
	fit := scenarioFit(t, querySeqJSON(t, dir, base+"Reassign", "--depth", "3"))
	if fit["status"] != "excluded" {
		t.Errorf("Reassign 内层应 excluded: %+v", fit)
	}
	if fit["expected"] != "US" {
		t.Errorf("期望值应来自最后定义 US: %+v", fit)
	}
	if src, _ := fit["source"].(string); src == "" {
		t.Errorf("应记录定义位置/传播依据: %+v", fit)
	}
	// ConditionalReassign：合流未知 → unknown
	fit = scenarioFit(t, querySeqJSON(t, dir, base+"ConditionalReassign", "--depth", "3"))
	if fit["status"] != "unknown" {
		t.Errorf("条件合流应 unknown: %+v", fit)
	}
	// Dispatch / DispatchDynamic 基线
	if fit = scenarioFit(t, querySeqJSON(t, dir, base+"Dispatch", "--depth", "3")); fit["status"] != "taken" {
		t.Errorf("Dispatch 应 taken: %+v", fit)
	}
	if fit = scenarioFit(t, querySeqJSON(t, dir, base+"DispatchDynamic", "--depth", "3")); fit["status"] == "taken" {
		t.Errorf("DispatchDynamic 应 unknown: %+v", fit)
	}
}

func pickCall(t *testing.T, root map[string]any) map[string]any {
	t.Helper()
	var call map[string]any
	walkSeqJSON(root, func(n map[string]any) {
		if call != nil {
			return
		}
		if lbl, _ := n["Label"].(string); lbl == "d.Do" {
			call = n
		}
	})
	if call == nil {
		t.Fatalf("未找到 d.Do 调用: %+v", root)
	}
	return call
}

func TestTodo12SceneImplElseRealIndex(t *testing.T) {
	dir := setupTodo12SceneRepo(t)
	base := "symbol:go:example.com/codeintel-regression:Pick"
	us := pickCall(t, querySeqJSON(t, dir, base, "--depth", "2", "--scene", "platform=US"))
	if us["ImplType"] != "codeintel-regression.ImplUS" {
		t.Errorf("US 场景应选 ImplUS，实为 %v", us["ImplType"])
	}
	ev, _ := us["evidence"].(map[string]any)
	if ev == nil || !strings.HasPrefix(fmtStr(ev["reason"]), "scene-guard:") {
		t.Errorf("应记录场景守卫依据: %+v", ev)
	}
	if ev != nil && ev["ambiguous"] == true {
		t.Errorf("US 已唯一确定，不应 ambiguous: %+v", ev)
	}
	// 无场景：候选 + ambiguous
	plain := pickCall(t, querySeqJSON(t, dir, base, "--depth", "2"))
	pev, _ := plain["evidence"].(map[string]any)
	if pev == nil || pev["ambiguous"] != true {
		t.Errorf("无场景应 ambiguous: %+v", pev)
	}
	if cands, _ := pev["candidates"].([]any); len(cands) < 2 {
		t.Errorf("无场景应保留候选集合: %+v", pev)
	}
}

func fmtStr(v any) string {
	s, _ := v.(string)
	return s
}
