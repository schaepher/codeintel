//go:build integration

package integration

// todo36：B09e.4 包装函数 field_read 重绑定与调用方改写失效。

import (
	"strings"
	"testing"
)

// u36OneBody 取单动作的 request_body.id。
func u36OneBody(t *testing.T, dir, root string) map[string]any {
	t.Helper()
	acts := u35Actions(t, dir, root)
	if len(acts) != 1 {
		t.Fatalf("%s actions = %v", root, acts)
	}
	body, _ := acts[0]["request_body"].(map[string]any)
	return body
}

func u36CheckBody(t *testing.T, body map[string]any, kind string, param int, path ...string) {
	t.Helper()
	id, _ := body["id"].(map[string]any)
	if id == nil || asString(id["kind"]) != kind {
		t.Fatalf("body.id = %v（want kind=%s）", body["id"], kind)
	}
	if kind == "unknown" {
		if id["parameter"] != nil || len(asAnySlice(id["path"])) != 0 {
			t.Fatalf("unknown 仍残留 parameter/path: %v", id)
		}
		return
	}
	if id["parameter"] == nil || u35Int(id["parameter"]) != param {
		t.Fatalf("parameter = %v（want %d）", id["parameter"], param)
	}
	var got []string
	for _, v := range asAnySlice(id["path"]) {
		got = append(got, asString(v))
	}
	if strings.Join(got, ".") != strings.Join(path, ".") {
		t.Fatalf("path = %v（want %v）", got, path)
	}
}

// TestFrontendWrappedFieldParameters 包装函数重绑定 field_read 根参数。
func TestFrontendWrappedFieldParameters(t *testing.T) {
	dir := u36Repo(t)
	t.Run("positive", func(t *testing.T) {
		acts := u35Actions(t, dir, "wrapper-field")
		if len(acts) != 2 {
			t.Fatalf("wrapper-field actions = %v", acts)
		}
		for _, act := range acts {
			if asString(act["action"]) == "click" {
				u36CheckBody(t, act["request_body"].(map[string]any), "field_read", 1, "id")
			}
		}
	})
	t.Run("reordered", func(t *testing.T) {
		u36CheckBody(t, u36OneBody(t, dir, "wrapper-reordered"), "field_read", 0, "id")
	})
}

// TestFrontendWrappedFieldInvalidation 调用前改写失效；请求之后改写不倒推。
func TestFrontendWrappedFieldInvalidation(t *testing.T) {
	dir := u36Repo(t)
	t.Run("positive", func(t *testing.T) {
		u36CheckBody(t, u36OneBody(t, dir, "wrapper-after"), "field_read", 1, "id")
	})
	t.Run("negative", func(t *testing.T) {
		acts := u35Actions(t, dir, "wrapper-field")
		for _, act := range acts {
			if asString(act["action"]) == "clickMutated" {
				u36CheckBody(t, act["request_body"].(map[string]any), "unknown", 0)
			}
		}
		for _, root := range []string{"wrapper-whole", "wrapper-parent", "wrapper-shadow",
			"wrapper-index"} {
			u36CheckBody(t, u36OneBody(t, dir, root), "unknown", 0)
		}
	})
}

// TestFrontendWrappedFieldPaths 解构/嵌套路径经多层包装；同名文件不串接。
func TestFrontendWrappedFieldPaths(t *testing.T) {
	dir := u36Repo(t)
	t.Run("positive", func(t *testing.T) {
		u36CheckBody(t, u36OneBody(t, dir, "wrapper-destructure"), "field_read", 1, "id")
	})
	t.Run("negative", func(t *testing.T) {
		body := u36OneBody(t, dir, "wrapper-same-name")
		u36CheckBody(t, body, "field_read", 0, "id")
		acts := u35Actions(t, dir, "wrapper-same-name")
		if len(acts) != 1 || asString(acts[0]["path"]) != "/alternate" {
			t.Fatalf("same-name = %v（want /alternate）", acts)
		}
	})
}
