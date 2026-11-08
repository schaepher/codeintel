//go:build integration

package integration

// todo36：B03e.4 ORM 忽略字段/重复列/传播。

import "testing"

// u36Columns ORMUpdate → entry 的 read_column 列集合。
func u36Columns(t *testing.T, dir, entry string) []string {
	t.Helper()
	root := u35Query(t, dir, "ORMUpdate", "--related-entry", u35ReviewArg(entry))
	var out []string
	seen := map[string]bool{}
	for _, raw := range asAnySlice(root["workflow"].(map[string]any)["entries"]) {
		e, _ := raw.(map[string]any)
		var walk func(any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				if cond, ok := x["condition"].(map[string]any); ok {
					if left, ok := cond["left"].(map[string]any); ok {
						if rc, ok := left["read_column"].(map[string]any); ok {
							col := asString(rc["column"])
							if col != "" && !seen[col] {
								seen[col] = true
								out = append(out, col)
							}
						}
					}
				}
				for _, vv := range x {
					walk(vv)
				}
			case []any:
				for _, vv := range x {
					walk(vv)
				}
			}
		}
		walk(e["sequence"])
	}
	return out
}

// TestORMIgnoredFieldOrigins 映射字段保留 kind；xorm:"-" 不产生列来源。
func TestORMIgnoredFieldOrigins(t *testing.T) {
	dir := u36Repo(t)
	t.Run("mapped", func(t *testing.T) {
		cols := u36Columns(t, dir, "ORMNamedFind")
		if len(cols) != 1 || cols[0] != "kind" {
			t.Fatalf("ORMNamedFind columns = %v（want [kind]）", cols)
		}
	})
	t.Run("ignored", func(t *testing.T) {
		if cols := u36Columns(t, dir, "ORMIgnoredFind"); len(cols) != 0 {
			t.Fatalf("ORMIgnoredFind columns = %v（want []）", cols)
		}
	})
}

// TestORMColumnMappingAmbiguity 唯一映射可用；重复列不能任取一个。
func TestORMColumnMappingAmbiguity(t *testing.T) {
	dir := u36Repo(t)
	t.Run("unique", func(t *testing.T) {
		cols := u36Columns(t, dir, "ORMNamedFind")
		if len(cols) != 1 || cols[0] != "kind" {
			t.Fatalf("unique columns = %v（want [kind]）", cols)
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		if cols := u36Columns(t, dir, "ORMAmbiguousFind"); len(cols) != 0 {
			t.Fatalf("ambiguous columns = %v（want []）", cols)
		}
	})
}

// TestORMIgnoredFieldPropagation 忽略字段经 helper 返回 + range 仍不是列来源。
func TestORMIgnoredFieldPropagation(t *testing.T) {
	dir := u36Repo(t)
	t.Run("positive", func(t *testing.T) {
		cols := u36Columns(t, dir, "ORMNamedFind")
		if len(cols) != 1 || cols[0] != "kind" {
			t.Fatalf("mapped columns = %v", cols)
		}
	})
	t.Run("negative", func(t *testing.T) {
		for _, entry := range []string{"ReviewIgnoredDAO", "ORMIgnoredFind", "ORMAllIgnoredFind"} {
			if cols := u36Columns(t, dir, entry); len(cols) != 0 {
				t.Fatalf("%s columns = %v（want []）", entry, cols)
			}
		}
	})
}
