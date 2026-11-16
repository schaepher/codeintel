//go:build integration

package integration

// todo37：B09e.4 调用方解构别名的来源传递（按声明绑定 + 路径合并 + 改写失效）。

import (
	"strings"
	"testing"
)

// u37Actions 前端动作 → {action: action}。
func u37Actions(t *testing.T, dir, root string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, act := range u35Actions(t, dir, root) {
		out[asString(act["action"])] = act
	}
	return out
}

// u37CheckBody 断言 request_body.id 的来源；unknown 不得残留 parameter/path。
func u37CheckBody(t *testing.T, act map[string]any, kind string, param int, path ...string) {
	t.Helper()
	body, _ := act["request_body"].(map[string]any)
	id, _ := body["id"].(map[string]any)
	if id == nil || asString(id["kind"]) != kind {
		t.Fatalf("action=%s body.id = %v（want kind=%s）", asString(act["action"]),
			body["id"], kind)
	}
	if kind == "unknown" {
		if id["parameter"] != nil || len(asAnySlice(id["path"])) != 0 {
			t.Fatalf("action=%s unknown 仍残留 parameter/path: %v", asString(act["action"]), id)
		}
		return
	}
	if id["parameter"] == nil || u35Int(id["parameter"]) != param {
		t.Fatalf("action=%s parameter = %v（want %d）", asString(act["action"]),
			id["parameter"], param)
	}
	var got []string
	for _, v := range asAnySlice(id["path"]) {
		got = append(got, asString(v))
	}
	if strings.Join(got, ".") != strings.Join(path, ".") {
		t.Fatalf("action=%s path = %v（want %v）", asString(act["action"]), got, path)
	}
}

// u37One 单动作目录的动作。
func u37One(t *testing.T, dir, root, action string) map[string]any {
	t.Helper()
	acts := u37Actions(t, dir, root)
	act := acts[action]
	if act == nil || len(acts) != 1 {
		t.Fatalf("%s actions = %v（want 唯一 %s）", root, acts, action)
	}
	return act
}

// TestFrontendCallerDestructureBindings 调用方解构声明 → 实参根对象绑定。
func TestFrontendCallerDestructureBindings(t *testing.T) {
	dir := u37Repo(t)
	t.Run("positive", func(t *testing.T) {
		acts := u37Actions(t, dir, "caller-destructure")
		if len(acts) != 3 {
			t.Fatalf("caller-destructure actions = %v", acts)
		}
		// 同一参数的两种取值写法必须给出相同来源。
		u37CheckBody(t, acts["click"], "field_read", 1, "profile", "id")
		u37CheckBody(t, acts["clickPlain"], "field_read", 1, "profile", "id")
		// 交换形参后位置随真实声明变化。
		u37CheckBody(t, u37One(t, dir, "caller-destructure-swapped", "click"),
			"field_read", 0, "profile", "id")
	})
	t.Run("negative", func(t *testing.T) {
		// 局部同名遮蔽 / 局部另一对象 / 跨文件同名变量 / 重赋值别名。
		for _, c := range []struct{ root, action string }{
			{"caller-destructure-shadow", "clickShadow"},
			{"caller-destructure-local", "clickLocal"},
			{"caller-destructure-crossfile", "clickCross"},
			{"caller-destructure-parent", "clickParent"},
		} {
			u37CheckBody(t, u37One(t, dir, c.root, c.action), "unknown", 0)
		}
	})
}

// TestFrontendCallerDestructurePaths 解构路径与实参字段路径逐次合并。
func TestFrontendCallerDestructurePaths(t *testing.T) {
	dir := u37Repo(t)
	t.Run("positive", func(t *testing.T) {
		u37CheckBody(t, u37One(t, dir, "caller-destructure-nested", "clickNested"),
			"field_read", 1, "outer", "profile", "id")
	})
	t.Run("negative", func(t *testing.T) {
		// 传另一子对象：路径必须变成 other（不能重复 profile）。
		u37CheckBody(t, u37One(t, dir, "caller-destructure-other", "clickOther"),
			"field_read", 1, "other", "id")
		acts := u37Actions(t, dir, "caller-destructure-badpath")
		u37CheckBody(t, acts["clickIndex"], "unknown", 0)
		u37CheckBody(t, acts["clickMissing"], "unknown", 0)
	})
}

// TestFrontendCallerDestructureInvalidation 真实调用位置的改写失效。
func TestFrontendCallerDestructureInvalidation(t *testing.T) {
	dir := u37Repo(t)
	t.Run("positive", func(t *testing.T) {
		// 未改写保持精确来源；请求之后的改写不倒推；无关兄弟字段不污染。
		acts := u37Actions(t, dir, "caller-destructure")
		u37CheckBody(t, acts["click"], "field_read", 1, "profile", "id")
		u37CheckBody(t, u37One(t, dir, "caller-destructure-after", "clickAfter"),
			"field_read", 1, "profile", "id")
		u37CheckBody(t, u37One(t, dir, "caller-destructure-sibling", "clickSibling"),
			"field_read", 1, "profile", "id")
	})
	t.Run("negative", func(t *testing.T) {
		// 调用前改写别名字段 / 重赋整个别名 / 覆盖父对象。
		acts := u37Actions(t, dir, "caller-destructure")
		u37CheckBody(t, acts["clickMutated"], "unknown", 0)
		u37CheckBody(t, u37One(t, dir, "caller-destructure-parent", "clickParent"),
			"unknown", 0)
	})
}

// TestFrontendCallerDestructureIdentity 多别名/同名声明/跨文件被调函数身份。
func TestFrontendCallerDestructureIdentity(t *testing.T) {
	dir := u37Repo(t)
	t.Run("positive", func(t *testing.T) {
		acts := u37Actions(t, dir, "caller-destructure-identity")
		if len(acts) != 2 {
			t.Fatalf("identity actions = %v", acts)
		}
		// 每个别名按自己的声明路径解析；被调函数在另一文件仍解析到 /requests。
		for name, path := range map[string][]string{
			"clickA": {"profile", "id"},
			"clickB": {"other", "id"},
		} {
			act := acts[name]
			u37CheckBody(t, act, "field_read", 1, path...)
			if asString(act["path"]) != "/requests" {
				t.Fatalf("%s path = %v（want /requests）", name, act["path"])
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		// 同名不同声明：只解析实际导入文件（b.ts 的 other 路径）。
		u37CheckBody(t, u37One(t, dir, "caller-destructure-samename", "click"),
			"field_read", 1, "other", "id")
		// 多别名改写：根名取真实实参，每次查询都必须一致地 unknown。
		for i := 0; i < 8; i++ {
			u37CheckBody(t, u37One(t, dir, "caller-destructure-multi", "clickMutateB"),
				"unknown", 0)
		}
	})
}
