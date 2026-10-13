package cli

// 索引重建安全与标识约束回归（Q?）：
//   - clean 明示删除范围（人工规则/问答/时序图），--force 只跳过确认；
//   - 对外 JSON 输出不泄露整数代理键（id_int/source_ref/target_ref）。

import (
	"strings"
	"testing"
)

// TestCleanDeletionScopeNotice：clean 明示删除范围（含人工数据与时序图）；
// --force 跳过确认但仍打印范围。
func TestCleanDeletionScopeNotice(t *testing.T) {
	dir := seedRepo(t)
	out := captureStdout(func() {
		if code := cmdClean([]string{"--repo", dir, "--force"}); code != 0 {
			t.Errorf("clean exit = %d", code)
		}
	})
	for _, want := range []string{"删除范围", "relation_rules", "qa_history", "sequence"} {
		if !strings.Contains(out, want) {
			t.Errorf("clean 删除告知应含 %q:\n%s", want, out)
		}
	}
}

// TestQueryJSONNoIntegerProxyKeys：对外节点/端点 JSON 不泄露数据库内部
// 整数代理键（id_int / source_ref / target_ref 仅库内使用）。
func TestQueryJSONNoIntegerProxyKeys(t *testing.T) {
	dir := seedRepo(t)
	for _, args := range [][]string{
		{"symbol", "main", "--repo", dir, "--json"},
		{"callers", "symbol:go:example.com/m/svc:(Svc).Run", "--repo", dir, "--json"},
		{"callees", "main", "--repo", dir, "--json"},
	} {
		out := captureStdout(func() {
			if code := cmdQuery(args); code != 0 {
				t.Errorf("query %v exit = %d", args, code)
			}
		})
		for _, bad := range []string{`"id_int"`, `"source_ref"`, `"target_ref"`} {
			if strings.Contains(out, bad) {
				t.Errorf("query %v JSON 泄露内部键 %s:\n%s", args, bad, out)
			}
		}
	}
}
