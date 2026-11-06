//go:build integration

package integration

import (
	"strings"
	"testing"
)

// u34Branch 入口**自身**语句树里找指定 label 的分支（不进入被调函数体）。
// 返回该分支的 condition（可能为 nil：操作数来源未证明）与是否找到分支。
func u34Branch(t *testing.T, root map[string]any, entry, label string) (map[string]any, bool) {
	t.Helper()
	wf, _ := root["workflow"].(map[string]any)
	for _, raw := range asAnySlice(wf["entries"]) {
		e, _ := raw.(map[string]any)
		if !strings.HasSuffix(asString(e["symbol"]), entry) {
			continue
		}
		var found map[string]any
		hit := false
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, nr := range nodes {
				n, _ := nr.(map[string]any)
				if n["Kind"] == "call" {
					// 被调函数的体不属于本入口语句（其内部分支单独判断）。
					continue
				}
				if n["Kind"] == "branch" && n["Label"] == label {
					found, _ = n["condition"].(map[string]any)
					hit = true
				}
				if hit {
					return
				}
				walk(asAnySlice(n["Nodes"]))
				walk(asAnySlice(n["Else"]))
			}
		}
		seq, _ := e["sequence"].(map[string]any)
		walk(asAnySlice(seq["Nodes"]))
		if hit {
			return found, true
		}
	}
	t.Fatalf("入口 %s 未找到分支 %q", entry, label)
	return nil, false
}

// u34BranchLeft 分支条件左操作数（分支不存在/操作数未知 → nil）。
func u34BranchLeft(t *testing.T, root map[string]any, entry, label string) map[string]any {
	t.Helper()
	cond, _ := u34Branch(t, root, entry, label)
	left, _ := cond["left"].(map[string]any)
	return left
}

// B03e.1 实际签名槽：Find 的槽 0 是 error，Get 的 error 在槽 1。
func TestORMReadSignatureSlots(t *testing.T) {
	dir := u34Repo(t)
	root := u34Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewDAO")
	left := u34BranchLeft(t, root, "ReviewDAO", "err!=nil")
	if asString(left["kind"]) != "read_error" || left["read_result"] != nil {
		t.Fatalf("Find error 槽 = %v（want read_error 且无 read_result）", left)
	}
	one := u34Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewOne")
	leftOne := u34BranchLeft(t, one, "ReviewOne", "err!=nil")
	if asString(leftOne["kind"]) != "read_error" || leftOne["read_result"] != nil {
		t.Fatalf("Get error 槽 = %v（want read_error 且无 read_result）", leftOne)
	}
	// Get 的 bool 槽不能标成 sql_read_result（模型/集合身份不借用）。
	if boolLeft := u34BranchLeft(t, one, "ReviewOne", "!ok"); asString(boolLeft["kind"]) == "sql_read_result" {
		t.Fatalf("Get bool 槽被标成读取结果: %v", boolLeft)
	}
}

// B03e.2 错误身份：错误被覆盖后不能借用本次读取。
func TestORMReadErrorIdentity(t *testing.T) {
	dir := u34Repo(t)
	root := u34Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewDAO")
	if k := asString(u34BranchLeft(t, root, "ReviewDAO", "err!=nil")["kind"]); k != "read_error" {
		t.Fatalf("positive read_error = %q", k)
	}
	root = u34Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewOverwrite")
	if k := asString(u34BranchLeft(t, root, "ReviewOverwrite", "err!=nil")["kind"]); k == "read_error" {
		t.Fatalf("negative 覆盖错误后仍借用 read_error")
	}
}

// B03e.3 集合及元素：集合来源沿返回槽；返回另一集合并不能借用列来源。
func TestORMReadCollectionIdentity(t *testing.T) {
	dir := u34Repo(t)
	root := u34Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewDAO")
	left := u34BranchLeft(t, root, "ReviewDAO", `r.Kind=="profile"`)
	rc, _ := left["read_column"].(map[string]any)
	if asString(rc["column"]) != "kind" {
		t.Fatalf("positive r.Kind read_column = %v（want kind）", left)
	}
	root = u34Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewWrong")
	left = u34BranchLeft(t, root, "ReviewWrong", `r.Kind=="profile"`)
	if left["read_column"] != nil {
		t.Fatalf("negative 返回另一集合仍借用列来源: %v", left)
	}
}

// B03e.4 模型与命名：模型列按标签解析（真实声明），不套用其它模型。
func TestORMReadModelIdentity(t *testing.T) {
	dir := u34Repo(t)
	root := u34Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ReviewDAO")
	left := u34BranchLeft(t, root, "ReviewDAO", `r.Kind=="profile"`)
	rc, _ := left["read_column"].(map[string]any)
	if asString(rc["column"]) != "kind" {
		t.Fatalf("模型列 = %v（want kind）", left)
	}
}
