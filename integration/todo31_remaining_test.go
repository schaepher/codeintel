//go:build integration

package integration

// todo31：剩余连接项（U04bR/U04hR/U04fR、B02b/B03a/B03b、B05b/B06b、
// B09b/B10b/B11b）——源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func u31Query(t *testing.T, dir, entry string, extra ...string) map[string]any {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/remaining:" + entry,
		"--repo", dir, "--code", "--depth", "12", "--json"}
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

// u31ReadColumn 入口树里指定标签 branch 的 read_column 列名（空 = 未命中）。
func u31ReadColumn(root map[string]any, label string) string {
	col := ""
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if n["Kind"] == "branch" && n["Label"] == label {
				c, _ := n["condition"].(map[string]any)
				l, _ := c["left"].(map[string]any)
				rc, _ := l["read_column"].(map[string]any)
				if s := asString(rc["column"]); s != "" {
					col = s
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
	return col
}

// u31Connection 取第一/指定 from→to 连接。
func u31Connection(wf map[string]any, from, to string) map[string]any {
	conns, _ := wf["connections"].([]any)
	for _, raw := range conns {
		c, _ := raw.(map[string]any)
		f, _ := c["from"].(map[string]any)
		t, _ := c["to"].(map[string]any)
		if seqLastColon(f["entry"]) == from && seqLastColon(t["entry"]) == to {
			return c
		}
	}
	return nil
}

func TestU31U04bRResourceAndPaths(t *testing.T) {
	dir := u31Repo(t)
	runtimePath := filepath.Join(dir, "runtime.json")
	// 资源被 Reset 改写 → 不连接。
	root := u31Query(t, dir, "SubmitKnown", "--related-entry",
		"symbol:go:example.com/remaining:ReviewRebound", "--runtime-data", runtimePath)
	wf, _ := root["workflow"].(map[string]any)
	if conns, _ := wf["connections"].([]any); len(conns) != 0 {
		t.Fatalf("binding_mutated 后仍连接: %v", conns)
	}
	// 正常：from/to.path 指向实际 SQL 节点路径。
	root = u31Query(t, dir, "Submit", "--related-entry",
		"symbol:go:example.com/remaining:Review")
	wf, _ = root["workflow"].(map[string]any)
	c := u31Connection(wf, "Submit", "Review")
	if c == nil {
		t.Fatalf("缺少 Submit→Review 连接")
	}
	from, _ := c["from"].(map[string]any)
	to, _ := c["to"].(map[string]any)
	if from["path"] != "/Nodes/0" || to["path"] != "/Nodes/0" {
		t.Fatalf("from/to path = %v/%v（want /Nodes/0）", from["path"], to["path"])
	}
}

func TestU31U04hRFieldKeys(t *testing.T) {
	dir := u31Repo(t)
	root := u31Query(t, dir, "Submit", "--related-entry",
		"symbol:go:example.com/remaining:ReviewTwo")
	wf, _ := root["workflow"].(map[string]any)
	conns, _ := wf["connections"].([]any)
	insts, _ := wf["instances"].([]any)
	n := 0
	for _, raw := range insts {
		i, _ := raw.(map[string]any)
		if i["kind"] == "conditional_external_request" {
			n++
		}
	}
	if len(conns) != 2 || n != 2 {
		t.Fatalf("connections/external_requests = %d/%d（want 2/2）", len(conns), n)
	}
	// 两个字段来源不同：分组签名不能合并。
	if seqWFKey(firstStr(conns[0])) == seqWFKey(firstStr(conns[1])) {
		t.Fatalf("不同字段的连接被合并: %v", conns)
	}
}

// firstStr 取连接第一把键的来源键文本（辅助）。
func firstStr(v any) map[string]any {
	c, _ := v.(map[string]any)
	keys, _ := c["keys"].([]any)
	if len(keys) == 0 {
		return nil
	}
	k, _ := keys[0].(map[string]any)
	req, _ := k["requested"].(map[string]any)
	return req
}

func seqWFKey(m map[string]any) string {
	o, _ := m["origin"].(map[string]any)
	if o == nil {
		return ""
	}
	fr, _ := o["field_read"].(map[string]any)
	if fr == nil {
		return asString(o["kind"])
	}
	path, _ := fr["path"].([]any)
	var parts []string
	for _, p := range path {
		parts = append(parts, asString(p))
	}
	return asString(o["kind"]) + ":" + strings.Join(parts, ".")
}

func TestU31B02bCrossPackage(t *testing.T) {
	dir := u31Repo(t)
	root := u31Query(t, dir, "CrossPackage")
	if col := u31ReadColumn(root, `record.Kind=="profile"`); col != "kind" {
		t.Fatalf("跨包只读调用列来源 = %q（want kind）", col)
	}
}

func TestU31B03aNestedPointerAliasEmbedded(t *testing.T) {
	dir := u31Repo(t)
	cases := []struct{ entry, label string }{
		{"Pointer", `record.Detail.Kind=="profile"`},
		{"AliasRoot", `record.Kind=="profile"`},
		{"Embedded", `record.Kind=="profile"`},
	}
	for _, c := range cases {
		root := u31Query(t, dir, c.entry)
		if col := u31ReadColumn(root, c.label); col != "kind" {
			t.Fatalf("%s 列来源 = %q（want kind）", c.entry, col)
		}
	}
	// 反例：中间指针重绑 / 整值覆写 / 全局对象。
	for _, entry := range []string{"PointerRebind", "PointerOverwrite", "GlobalPointer"} {
		root := u31Query(t, dir, entry)
		if col := u31ReadColumn(root, `record.Detail.Kind=="profile"`); col != "" {
			t.Fatalf("%s 不应保留列来源（%q）", entry, col)
		}
		if entry == "GlobalPointer" {
			if col := u31ReadColumn(root, `GlobalEnv.Detail.Kind=="profile"`); col != "" {
				t.Fatalf("全局对象不应保留列来源（%q）", col)
			}
		}
	}
}

func TestU31B03bORMUpdateFind(t *testing.T) {
	dir := u31Repo(t)
	root := u31Query(t, dir, "ORMUpdate", "--related-entry",
		"symbol:go:example.com/remaining:ORMFind")
	wf, _ := root["workflow"].(map[string]any)
	conns, _ := wf["connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("ORM Update/Find connections = %d（want 1）", len(conns))
	}
}
