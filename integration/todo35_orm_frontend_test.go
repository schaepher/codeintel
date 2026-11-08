//go:build integration

package integration

// todo35：B03e.4 + B09e.3/.4（ORM 表身份 / 前端声明与字段来源）。

import (
	"strings"
	"testing"
)

// TestORMStaticModelTableIdentity B03e.4：静态 TableName 进入 ORM 读写连接。
func TestORMStaticModelTableIdentity(t *testing.T) {
	dir := u35Repo(t)
	t.Run("positive", func(t *testing.T) {
		root := u35Query(t, dir, "ORMUpdate", "--related-entry", u35ReviewArg("ORMNamedFind"))
		if n := u34ConnCount(root); n != 1 {
			t.Fatalf("ORMNamedFind connections = %d（want 1）", n)
		}
		c := u34Conn(root, "ORMUpdate", "ORMNamedFind")
		if c == nil || asString(c["table"]) != "nodes" {
			t.Fatalf("连接表身份错误: %v", c)
		}
	})
	t.Run("negative", func(t *testing.T) {
		for _, entry := range []string{"ORMNamedFindOther", "ORMNamedFindDynamic",
			"ORMNamedFindNoName", "ORMNamedFindAlias"} {
			root := u35Query(t, dir, "ORMUpdate", "--related-entry", u35ReviewArg(entry))
			if n := u34ConnCount(root); n != 0 {
				t.Fatalf("%s connections = %d（want 0）", entry, n)
			}
		}
	})
}

// TestORMModelIdentityVariants B03e.4 模型变体：标签列/显式 Table 继续工作。
func TestORMModelIdentityVariants(t *testing.T) {
	dir := u35Repo(t)
	t.Run("positive", func(t *testing.T) {
		root := u35Query(t, dir, "ORMUpdate", "--related-entry", u35ReviewArg("ORMNamedFind"))
		c := u34Conn(root, "ORMUpdate", "ORMNamedFind")
		if c == nil {
			t.Fatal("缺少 ORM 读写连接")
		}
		found := false
		for _, raw := range asAnySlice(c["read_values"]) {
			rv, _ := raw.(map[string]any)
			if asString(rv["column"]) == "kind" {
				found = true
			}
		}
		if !found {
			t.Fatalf("kind 列来源缺失: %v", c["read_values"])
		}
	})
	t.Run("negative", func(t *testing.T) {
		// 未知链修饰器（Alias）改变语义 → 不借用显式/模型表身份。
		root := u35Query(t, dir, "ORMUpdate", "--related-entry", u35ReviewArg("ORMNamedFindAlias"))
		if n := u34ConnCount(root); n != 0 {
			t.Fatalf("ORMNamedFindAlias connections = %d（want 0）", n)
		}
	})
}

// u35Actions 查询前端动作列表。
func u35Actions(t *testing.T, dir, root string) []map[string]any {
	t.Helper()
	q := u35Query(t, dir, "SubmitHTTP", "--related-entry", u35ReviewArg("ReviewHTTP"),
		"--frontend-root", root)
	wf, _ := q["workflow"].(map[string]any)
	front, _ := wf["frontend"].(map[string]any)
	var out []map[string]any
	for _, raw := range asAnySlice(front["actions"]) {
		m, _ := raw.(map[string]any)
		out = append(out, m)
	}
	return out
}

// TestFrontendBoundedWrapperResolution B09e.3：有界解析长包装链/递归/预算。
func TestFrontendBoundedWrapperResolution(t *testing.T) {
	dir := u35Repo(t)
	t.Run("positive", func(t *testing.T) {
		acts := u35Actions(t, dir, "deep-frontend")
		if len(acts) != 1 || asString(acts[0]["path"]) != "/requests" ||
			asString(acts[0]["method"]) != "POST" ||
			asString(acts[0]["entry"]) != "symbol:go:example.com/remaining:SubmitHTTP" {
			t.Fatalf("deep-frontend = %v", acts)
		}
	})
	t.Run("negative", func(t *testing.T) {
		for _, c := range []struct{ root, reason string }{
			{"cycle-frontend", "declaration_cycle"},
			{"budget-frontend", "declaration_budget_exhausted"},
		} {
			acts := u35Actions(t, dir, c.root)
			if len(acts) != 1 || asString(acts[0]["unknown"]) != c.reason {
				t.Fatalf("%s = %v（want unknown=%s）", c.root, acts, c.reason)
			}
			if asString(acts[0]["action"]) == "" {
				t.Fatalf("%s 丢失真实动作: %v", c.root, acts)
			}
		}
		// 同名不同文件：只解析实际导入文件。
		acts := u35Actions(t, dir, "same-name")
		if len(acts) != 1 || asString(acts[0]["path"]) != "/alternate" {
			t.Fatalf("same-name = %v（want /alternate）", acts)
		}
	})
}

// u35Body 取第一个动作的 request_body。
func u35Body(t *testing.T, dir, root string) map[string]any {
	t.Helper()
	acts := u35Actions(t, dir, root)
	if len(acts) != 1 {
		t.Fatalf("%s actions = %v", root, acts)
	}
	body, _ := acts[0]["request_body"].(map[string]any)
	return body
}

// TestFrontendFieldAndDestructureOrigins B09e.4：字段/解构参数来源与反例。
func TestFrontendFieldAndDestructureOrigins(t *testing.T) {
	dir := u35Repo(t)
	check := func(t *testing.T, body map[string]any, kind string, path ...string) {
		t.Helper()
		id, _ := body["id"].(map[string]any)
		if id == nil || asString(id["kind"]) != kind {
			t.Fatalf("body.id = %v（want kind=%s）", body["id"], kind)
		}
		if id["parameter"] == nil || u35Int(id["parameter"]) != 0 {
			t.Fatalf("body.id.parameter = %v（want 0）", id["parameter"])
		}
		var got []string
		for _, v := range asAnySlice(id["path"]) {
			got = append(got, asString(v))
		}
		if strings.Join(got, ".") != strings.Join(path, ".") {
			t.Fatalf("body.id.path = %v（want %v）", got, path)
		}
	}
	t.Run("positive", func(t *testing.T) {
		check(t, u35Body(t, dir, "field-frontend"), "field_read", "id")
		check(t, u35Body(t, dir, "destructure-frontend"), "field_read", "id")
		check(t, u35Body(t, dir, "alias-field-frontend"), "field_read", "id")
		check(t, u35Body(t, dir, "nested-field-frontend"), "field_read", "user", "id")
	})
	t.Run("negative", func(t *testing.T) {
		for _, root := range []string{"other-field-frontend", "mutated-field-frontend",
			"index-field-frontend"} {
			id, _ := u35Body(t, dir, root)["id"].(map[string]any)
			if id == nil || asString(id["kind"]) != "unknown" {
				t.Fatalf("%s body.id = %v（want unknown）", root, u35Body(t, dir, root)["id"])
			}
		}
	})
}
