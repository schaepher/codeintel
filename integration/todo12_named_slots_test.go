//go:build integration

package integration

// todo12-12 A1：真实源码 → 生产索引 → CLI，多个具名返回值的槽身份。

import (
	"encoding/json"
	"testing"
)

func TestTodo12NamedResultSlotsRealIndex(t *testing.T) {
	dir := setupTodo12Repo(t)
	for _, fn := range []string{"Named", "Bare"} {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/effects:"+fn,
			"--repo", dir, "--code", "--depth", "2", "--json")
		if code != 0 {
			t.Fatalf("%s sequence exit = %d; %s", fn, code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("%s JSON: %v\n%s", fn, err, out)
		}
		var ret map[string]any
		var walk func(m map[string]any)
		walk = func(m map[string]any) {
			if m["Kind"] == "return" || m["kind"] == "return" {
				ret = m
				return
			}
			for _, key := range []string{"Nodes", "nodes"} {
				if ns, ok := m[key].([]any); ok {
					for _, x := range ns {
						if mm, ok := x.(map[string]any); ok {
							walk(mm)
						}
					}
				}
			}
		}
		walk(root)
		if ret == nil {
			t.Fatalf("%s 应输出 return 节点: %v", fn, root)
		}
		slots, _ := ret["slots"].([]any)
		if len(slots) != 2 || slots[0] != "state" || slots[1] != "reason" {
			t.Errorf("%s 结果槽 = %v; want [state reason]", fn, ret["slots"])
		}
	}
}
