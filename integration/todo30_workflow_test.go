//go:build integration

package integration

// todo30 U04b/U04f/U04g：条件化连接、请求实例与渲染——真实源码 → init → CLI。

import (
	"encoding/json"
	"strings"
	"testing"
)

func u30WorkflowJSON(t *testing.T, dir string, extra ...string) map[string]any {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/flow:Submit",
		"--related-entry", "symbol:go:example.com/flow:FinishReview",
		"--repo", dir, "--code", "--depth", "12", "--json"}
	args = append(args, extra...)
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	wf, _ := root["workflow"].(map[string]any)
	if wf == nil {
		t.Fatalf("缺少 workflow: %s", out)
	}
	return wf
}

func TestU30U04bConnections(t *testing.T) {
	dir := u30FlowRepo(t)
	wf := u30WorkflowJSON(t, dir)
	conns, _ := wf["connections"].([]any)
	if len(conns) != 2 {
		t.Fatalf("connections = %d（want 2）: %v", len(conns), conns)
	}
	type row struct {
		From, To, Key, Read, Verdict string
	}
	var got []row
	for _, raw := range conns {
		c, _ := raw.(map[string]any)
		from, _ := c["from"].(map[string]any)
		to, _ := c["to"].(map[string]any)
		keys, _ := c["keys"].([]any)
		vals, _ := c["read_values"].([]any)
		r := row{From: seqLastColon(from["entry"]), To: seqLastColon(to["entry"]),
			Verdict: asString(c["verdict"])}
		if len(keys) > 0 {
			k, _ := keys[0].(map[string]any)
			w, _ := k["written"].(map[string]any)
			o, _ := w["origin"].(map[string]any)
			r.Key = asString(o["kind"])
		}
		if len(vals) > 0 {
			v, _ := vals[0].(map[string]any)
			o, _ := v["origin"].(map[string]any)
			r.Read = asString(o["literal"])
		}
		got = append(got, r)
	}
	want := []row{{"Submit", "FinishReview", "parameter", `"profile"`, "conditional"},
		{"FinishReview", "FinishReview", "concat", `"service"`, "conditional"}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("connection[%d] = %+v（want %+v）", i, got[i], want[i])
		}
	}
}

func TestU30U04fInstances(t *testing.T) {
	dir := u30FlowRepo(t)
	wf := u30WorkflowJSON(t, dir)
	insts, _ := wf["instances"].([]any)
	if len(insts) != 3 {
		t.Fatalf("instances = %d（want 3）", len(insts))
	}
	type row struct {
		ID, Entry, Parent string
		Writes            []string
	}
	var got []row
	for _, raw := range insts {
		i, _ := raw.(map[string]any)
		r := row{ID: asString(i["id"]), Entry: seqLastColon(i["entry"]),
			Parent: asString(i["parent_id"])}
		writes, _ := i["writes"].([]any)
		for _, wr := range writes {
			w, _ := wr.(map[string]any)
			vals, _ := w["values"].([]any)
			for _, vr := range vals {
				v, _ := vr.(map[string]any)
				if v["column"] != "kind" {
					continue
				}
				o, _ := v["origin"].(map[string]any)
				r.Writes = append(r.Writes, asString(o["literal"]))
			}
		}
		got = append(got, r)
	}
	want := []row{{"instance-1", "Submit", "", []string{`"profile"`}},
		{"instance-2", "FinishReview", "instance-1", []string{`"service"`}},
		{"instance-3", "FinishReview", "instance-2", nil}}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Entry != want[i].Entry ||
			got[i].Parent != want[i].Parent ||
			strings.Join(got[i].Writes, ",") != strings.Join(want[i].Writes, ",") {
			t.Fatalf("instance[%d] = %+v（want %+v）", i, got[i], want[i])
		}
	}
	// 第二次请求的来源键 = 第一次实例的 id + "-service"。
	keys := ""
	for _, raw := range insts {
		i, _ := raw.(map[string]any)
		if i["id"] != "instance-3" {
			continue
		}
		seq, _ := i["sequence"].(map[string]any)
		_ = seq
		for _, rv := range i["read_values"].([]any) {
			v, _ := rv.(map[string]any)
			o, _ := v["origin"].(map[string]any)
			keys = asString(o["literal"])
		}
	}
	if keys != `"service"` {
		t.Fatalf("instance-3 条件化读取值 = %q", keys)
	}
}

func TestU30U04gInstanceRender(t *testing.T) {
	dir := u30FlowRepo(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--related-entry", "symbol:go:example.com/flow:FinishReview",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid exit=%d\n%s", code, out)
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  P") && strings.Contains(line, "->>") {
			if strings.Contains(line, ": selected root ") ||
				strings.Contains(line, ": conditional external request ") {
				lines = append(lines, strings.TrimSpace(line[strings.Index(line, ": ")+2:]))
			}
		}
	}
	want := []string{"selected root instance-1: Submit",
		"conditional external request instance-2: FinishReview",
		"conditional external request instance-3: FinishReview"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("实例消息行 = %v（want %v）\n%s", lines, want, out)
	}
	// 条件说明与第二次请求的 -service 键来源。
	for _, wantStr := range []string{`instance condition kind=="profile" = true`,
		`instance condition kind=="profile" = false`,
		`FinishReview.param[1]+"-service"`} {
		if !strings.Contains(out, wantStr) {
			t.Fatalf("Mermaid 缺少 %q:\n%s", wantStr, out)
		}
	}
	// 文本同一展示树。
	code, text := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--related-entry", "symbol:go:example.com/flow:FinishReview",
		"--repo", dir, "--code", "--depth", "12")
	if code != 0 {
		t.Fatalf("text exit=%d", code)
	}
	for _, wantStr := range []string{"== selected root instance-1: Submit ==",
		"== conditional external request instance-2: FinishReview ==",
		`instance condition kind=="profile" = true`,
		`instance condition kind=="profile" = false`} {
		if !strings.Contains(text, wantStr) {
			t.Fatalf("文本缺少 %q:\n%s", wantStr, text)
		}
	}
}

func seqLastColon(v any) string {
	s, _ := v.(string)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// TestU30U04WorkflowDepth：depth=1+续查与完整深度保持同一流程；Scan 错误退出生效。
func TestU30U04WorkflowDepth(t *testing.T) {
	dir := u30FlowRepo(t)
	proj := func(extra ...string) (int, []string, []string) {
		args := []string{"query", "sequence", "symbol:go:example.com/flow:Submit",
			"--related-entry", "symbol:go:example.com/flow:FinishReview",
			"--repo", dir, "--code", "--json"}
		args = append(args, extra...)
		code, out := runCLIOut(t, args...)
		if code != 0 {
			t.Fatalf("query exit=%d\n%s", code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		wf, _ := root["workflow"].(map[string]any)
		conns, _ := wf["connections"].([]any)
		insts, _ := wf["instances"].([]any)
		var entries, errors []string
		for _, raw := range insts {
			inst, _ := raw.(map[string]any)
			entries = append(entries, asString(inst["id"])+":"+seqLastColon(inst["entry"]))
			if inst["kind"] != "conditional_external_request" {
				continue
			}
			seq, _ := inst["sequence"].(map[string]any)
			nodes, _ := seq["Nodes"].([]any)
			var walk func(nodes []any)
			walk = func(nodes []any) {
				for _, nr := range nodes {
					n, _ := nr.(map[string]any)
					if n["Kind"] == "branch" && strings.HasPrefix(asString(n["Label"]), "err") {
						ch, _ := n["Nodes"].([]any)
						hasReturn := false
						for _, cr := range ch {
							c, _ := cr.(map[string]any)
							if c["Kind"] == "return" {
								hasReturn = true
							}
						}
						if hasReturn {
							errors = append(errors, asString(inst["id"]))
						}
					}
					if sub, ok := n["Nodes"].([]any); ok {
						walk(sub)
					}
					if sub, ok := n["Else"].([]any); ok {
						walk(sub)
					}
				}
			}
			walk(nodes)
		}
		return len(conns), entries, errors
	}
	fullConns, fullEntries, fullErr := proj("--depth", "12")
	shallowConns, shallowEntries, shallowErr := proj("--depth", "1", "--continue-segments", "32")
	if fullConns != shallowConns ||
		strings.Join(fullEntries, ",") != strings.Join(shallowEntries, ",") ||
		strings.Join(fullErr, ",") != strings.Join(shallowErr, ",") {
		t.Fatalf("depth 一致性失败: full=%d/%v/%v shallow=%d/%v/%v",
			fullConns, fullEntries, fullErr, shallowConns, shallowEntries, shallowErr)
	}
	// 两次独立审核请求都保留 Scan 错误退出。
	if len(fullErr) != 2 {
		t.Fatalf("Scan 错误退出的实例 = %v（want 2）", fullErr)
	}
}
