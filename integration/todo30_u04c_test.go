//go:build integration

package integration

// todo30 U04c：if 分支条件的结构化来源——真实源码 → init → CLI。
// 只验收来源身份（参数/字面量/覆盖/取址逃逸/逻辑嵌套/预算），不改变
// Scene 判定。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u30ConditionSrc = `package condition

func Hit() {}

func Entry(id string) {
	if id == "profile" {
		Hit()
	}
}

func Overwrite(id string) {
	id = "changed"
	if id == "profile" {
		Hit()
	}
}

func Length(id string) {
	if len(id) > 0 {
		Hit()
	}
}

func Escape(id string) {
	p := &id
	_ = p
	if id == "profile" {
		Hit()
	}
}

func Nested(a, b string) {
	if !(a == "x" || b != "y") {
		Hit()
	}
}
`

// u30BranchConditions 查一个入口的全部 branch 条件（按树顺序）。
func u30BranchConditions(t *testing.T, repo, entry string) []map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/condition:"+entry,
		"--repo", repo, "--code", "--depth", "8", "--json")
	if code != 0 {
		t.Fatalf("%s query exit=%d\n%s", entry, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("%s JSON: %v\n%s", entry, err, out)
	}
	var conds []map[string]any
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if n["Kind"] == "branch" {
				if c, ok := n["condition"].(map[string]any); ok {
					conds = append(conds, c)
				} else {
					conds = append(conds, nil)
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
	return conds
}

func TestU30U04cConditionOperands(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/condition\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30ConditionSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}

	// 参数 vs 字面量。
	conds := u30BranchConditions(t, dir, "Entry")
	if len(conds) != 1 {
		t.Fatalf("Entry branch 条件数 = %d（want 1）", len(conds))
	}
	c := conds[0]
	if c["operator"] != "==" {
		t.Fatalf("operator = %v（want ==）", c["operator"])
	}
	left, _ := c["left"].(map[string]any)
	if left["kind"] != "parameter" || left["symbol"] != "symbol:go:example.com/condition:Entry" ||
		left["parameter"] != float64(0) || left["scalar_type"] != "string" {
		t.Fatalf("left 不是入口参数来源: %v", left)
	}
	right, _ := c["right"].(map[string]any)
	if right["kind"] != "literal" || right["literal"] != `"profile"` || right["scalar_type"] != "string" {
		t.Fatalf("right 不是字符串字面量: %v", right)
	}
	if c["unknown"] != nil {
		t.Fatalf("左右都已知时不应有 unknown: %v", c["unknown"])
	}

	// 覆盖后使用新来源。
	c = u30BranchConditions(t, dir, "Overwrite")[0]
	left, _ = c["left"].(map[string]any)
	if left["kind"] != "literal" || left["literal"] != `"changed"` {
		t.Fatalf("覆盖后 left 应为新字面量: %v", left)
	}

	// 未解析操作数（len(id)）：left 省略、right 整数 0、unknown 保留。
	c = u30BranchConditions(t, dir, "Length")[0]
	if c["left"] != nil {
		t.Fatalf("len(id) 不应有 left 来源: %v", c["left"])
	}
	right, _ = c["right"].(map[string]any)
	if right["kind"] != "literal" || right["literal"] != "0" || right["scalar_type"] != "int" {
		t.Fatalf("len(id) > 0 的 right 应为整数 0: %v", right)
	}
	if c["unknown"] != "operand_origin_unproven" {
		t.Fatalf("unknown = %v（want operand_origin_unproven）", c["unknown"])
	}

	// 取址逃逸后不能恢复入口参数来源。
	c = u30BranchConditions(t, dir, "Escape")[0]
	if c["left"] != nil {
		t.Fatalf("取址逃逸后 left 不应保留参数来源: %v", c["left"])
	}
	if c["unknown"] != "operand_origin_unproven" {
		t.Fatalf("取址逃逸 unknown = %v", c["unknown"])
	}

	// 逻辑嵌套：! → || → 两个比较，否定位置保留。
	c = u30BranchConditions(t, dir, "Nested")[0]
	if c["operator"] != "!" {
		t.Fatalf("Nested operator = %v（want !）", c["operator"])
	}
	terms, _ := c["terms"].([]any)
	if len(terms) != 1 {
		t.Fatalf("! 的 terms 数 = %d（want 1）", len(terms))
	}
	inner, _ := terms[0].(map[string]any)
	if inner["operator"] != "||" {
		t.Fatalf("!(...) 内层 operator = %v（want ||）", inner["operator"])
	}
	orTerms, _ := inner["terms"].([]any)
	if len(orTerms) != 2 {
		t.Fatalf("|| 子表达式数 = %d（want 2）", len(orTerms))
	}
	first, _ := orTerms[0].(map[string]any)
	second, _ := orTerms[1].(map[string]any)
	if first["operator"] != "==" || second["operator"] != "!=" {
		t.Fatalf("逻辑子表达式运算符/顺序不符: %v / %v", first["operator"], second["operator"])
	}
}

// TestU30U04cConditionBudget：超过条件递归/节点上限时整项 unknown + condition_budget。
func TestU30U04cConditionBudget(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	expr := `a=="1"`
	for i := 2; i <= 21; i++ {
		expr = expr + ` && (a=="` + strconvItoa(i) + `")`
	}
	src := "package condition\n\nfunc Hit() {}\n\nfunc Deep(a string) {\n\tif " + expr + " {\n\t\tHit()\n\t}\n}\n"
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/condition\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	c := u30BranchConditions(t, dir, "Deep")[0]
	if c["operator"] != "unknown" || c["unknown"] != "condition_budget" {
		t.Fatalf("预算耗尽应整项 unknown/condition_budget: %v", c)
	}
	if c["terms"] != nil || c["left"] != nil || c["right"] != nil {
		t.Fatalf("预算耗尽不得保留部分逻辑表达式: %v", c)
	}
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
