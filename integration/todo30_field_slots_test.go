//go:build integration

package integration

// todo30 U04j/U04k/U04l/B01：Scan 字段槽（值模型/分配+别名/只读 getter/返回槽）
// ——真实源码 → init → CLI。
//
// 只验收条件操作数的来源类别与列名（不证明数据库实际返回值）；反例必须
// 保持未知或非 read_column。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func u30FieldRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/scanfield\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "model", "model.go"), u30FieldModelSrc)
	writeFile(t, filepath.Join(dir, "flow.go"), u30FieldSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u30BranchLeft 按标签取 branch 左操作数（找不到返回 nil）。
func u30BranchLeft(t *testing.T, dir, entry, label string) map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/scanfield:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("%s exit=%d\n%s", entry, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("%s JSON: %v\n%s", entry, err, out)
	}
	var found map[string]any
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if n["Kind"] == "branch" &&
				strings.EqualFold(strings.TrimSpace(asString(n["Label"])), label) {
				c, _ := n["condition"].(map[string]any)
				left, _ := c["left"].(map[string]any)
				if left == nil {
					left = map[string]any{"kind": nil}
				}
				if found == nil {
					found = left
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
	nodes, _ := root["Nodes"].([]any)
	walk(nodes)
	return found
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func u30ReadColumnOf(left map[string]any) (string, string, bool) {
	if left == nil || left["kind"] != "read_column" {
		return "", "", false
	}
	rc, _ := left["read_column"].(map[string]any)
	if rc == nil {
		return "", "", false
	}
	col := asString(rc["column"])
	when := asString(rc["when"])
	return col, when, col != ""
}

func TestU30U04jValueModelFields(t *testing.T) {
	dir := u30FieldRepo(t)
	label := `record.Kind=="profile"`
	for _, entry := range []string{"Value", "ValueLit", "ValueParam", "ModelField"} {
		col, when, ok := u30ReadColumnOf(u30BranchLeft(t, dir, entry, label))
		if !ok || col != "kind" || when != "scan_success" {
			t.Fatalf("%s 应为 read_column/kind/scan_success: col=%q when=%q left=%v",
				entry, col, when, u30BranchLeft(t, dir, entry, label))
		}
	}
	// 反例：指针根无分配证明 / 类型别名 / 嵌套路径 / 自定义 Scanner。
	for _, entry := range []string{"BadPointer", "AliasRoot", "Nested", "Custom"} {
		if _, _, ok := u30ReadColumnOf(u30BranchLeft(t, dir, entry, label)); ok {
			t.Fatalf("%s 不应生成 read_column", entry)
		}
	}
	// 条件分支/零次循环：单路径产生的列值不能升级为无条件来源。
	for _, entry := range []string{"Conditional", "Loop"} {
		left := u30BranchLeft(t, dir, entry, label)
		if left == nil {
			t.Fatalf("%s 缺少条件", entry)
		}
		if _, _, ok := u30ReadColumnOf(left); ok {
			t.Fatalf("%s 列值不应成为无条件来源: %v", entry, left)
		}
	}
}

func TestU30U04kAllocationAliases(t *testing.T) {
	dir := u30FieldRepo(t)
	label := `record.Kind=="profile"`
	if col, _, ok := u30ReadColumnOf(u30BranchLeft(t, dir, "PtrAlias", label)); !ok || col != "kind" {
		t.Fatalf("PtrAlias 应保留 read_column/kind")
	}
	// 反例：别名写入 / 整值覆写 / 传出 / 全局存储 / 另一个分配。
	for _, entry := range []string{"PtrAliasWrite", "PtrDerefWrite", "PtrEscape", "PtrGlobal"} {
		if _, _, ok := u30ReadColumnOf(u30BranchLeft(t, dir, entry, label)); ok {
			t.Fatalf("%s 不应继续使用旧列值", entry)
		}
	}
	if left := u30BranchLeft(t, dir, "PtrOther", `x.Kind=="profile"`); left != nil {
		if _, _, ok := u30ReadColumnOf(left); ok {
			t.Fatalf("另一个 new(Record) 不应借用列值: %v", left)
		}
	}
}

func TestU30U04lReadOnlyGetter(t *testing.T) {
	dir := u30FieldRepo(t)
	for _, entry := range []string{"Getter"} {
		left := u30BranchLeft(t, dir, entry, `record.Current()=="profile"`)
		if col, when, ok := u30ReadColumnOf(left); !ok || col != "kind" || when != "scan_success" {
			t.Fatalf("%s getter 条件应为 read_column/kind/scan_success: %v", entry, left)
		}
	}
	// 修改方法 / 返回其它对象 / 带实参：不再保留旧列值。
	for _, entry := range []string{"GetterMutated"} {
		if _, _, ok := u30ReadColumnOf(u30BranchLeft(t, dir, entry, `record.Current()=="profile"`)); ok {
			t.Fatalf("%s 修改后不应保留 read_column", entry)
		}
	}
	if _, _, ok := u30ReadColumnOf(u30BranchLeft(t, dir, "WrongGetter", `record.GetOther()=="profile"`)); ok {
		t.Fatalf("返回其它对象的 getter 不应借用 read_column")
	}
	// defer 实参立即求值保留；延迟闭包体不保留。
	if col, _, ok := u30ReadColumnOf(u30BranchLeft(t, dir, "DeferArg", `kind=="profile"`)); !ok || col != "kind" {
		t.Fatalf("defer 实参应保留列值")
	}
	if _, _, ok := u30ReadColumnOf(u30BranchLeft(t, dir, "DeferBody", `kind=="profile"`)); ok {
		t.Fatalf("延迟闭包体不应借用当前列值")
	}
	// B01：立即执行匿名函数返回 getter 结果，外层调用条件保留列值。
	if col, _, ok := u30ReadColumnOf(u30BranchLeft(t, dir, "Immediate", `kind=="profile"`)); !ok || col != "kind" {
		t.Fatalf("B01 Immediate 应通过返回槽保留列值")
	}
}
