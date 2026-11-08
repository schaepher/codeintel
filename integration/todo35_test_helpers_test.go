//go:build integration

package integration

// todo35 共用测试辅助。

import (
	"encoding/json"
	"testing"
)

// u35Query 入口 → 关联入口集合的代码级时序 JSON。
func u35Query(t *testing.T, dir, entry string, extra ...string) map[string]any {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/remaining:" + entry,
		"--repo", dir, "--code", "--depth", "16", "--json"}
	args = append(args, extra...)
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("%s exit=%d\n%s", entry, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("%s JSON: %v\n%s", entry, err, out)
	}
	return root
}

func u35ReviewArg(entry string) string {
	return "symbol:go:example.com/remaining:" + entry
}

func u35Bool(v any) bool {
	b, _ := v.(bool)
	return b
}

func u35Int(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
