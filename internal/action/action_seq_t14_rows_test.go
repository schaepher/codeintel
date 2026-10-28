package action

// T14：标量多行语义（冲突失败/相同重复行 unknown）与未证明 SQL 形态拒绝。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 标量查询多行冲突 → 查询失败（不挑第一行、无半棵树）。
func TestT14ScalarMultiRowConflictFails(t *testing.T) {
	src := `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

func Config(ctx context.Context) (int, error) {
	var enabled int
	err := DB.QueryRowContext(ctx, "SELECT DISTINCT node_type FROM review_config WHERE region = ?", "ID").Scan(&enabled)
	return enabled, err
}

func Manual() {}
func Auto()   {}

func Entry(ctx context.Context) {
	enabled, err := Config(ctx)
	if err != nil {
		return
	}
	if enabled == 1 {
		Manual()
	} else {
		Auto()
	}
}
`
	snapBody := strings.Replace(t14bRuntime, `"scope":{"region":"ID","node_type":2}`,
		`"scope":{"region":"ID"}`, 1)
	snapBody = strings.Replace(snapBody, `"complete":true,"rows":[{`+`"region":"ID","node_type":2,"enabled":1}]`,
		`"complete":true,"rows":[{"region":"ID","node_type":2,"enabled":1},{"region":"ID","node_type":3,"enabled":0}]`, 1)
	acts, dir, _ := seedT14b(t, src)
	snap := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(snap, []byte(snapBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// DISTINCT 形态本身未证明 → 保持未知（两分支）而不是套值；下面再用
	// 无 DISTINCT 的形态验证"多行冲突失败"。
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 6, RuntimeDataPath: snap})
	if err != nil {
		t.Fatalf("DISTINCT 不应让查询失败（应保持未知）: %v", err)
	}
	if len(seqCallNodesNamed(root, "Manual")) != 1 || len(seqCallNodesNamed(root, "Auto")) != 1 {
		t.Errorf("DISTINCT 形态应保持未知（两分支）: %v", labelsOf(root.Nodes))
	}

	// 去掉 DISTINCT → 多行且值不同 → 必须失败（stdout 为空由 CLI 保证）。
	conflictSrc := strings.Replace(src, "SELECT DISTINCT node_type", "SELECT node_type", 1)
	acts2, dir2, _ := seedT14b(t, conflictSrc)
	snap2 := filepath.Join(dir2, "runtime.json")
	_ = os.WriteFile(snap2, []byte(snapBody), 0o644)
	root2, err := acts2.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir2, Depth: 6, RuntimeDataPath: snap2})
	if err == nil || root2 != nil {
		t.Fatalf("标量查询多行冲突必须失败且不返回树（root=%v err=%v）", root2, err)
	}
	if !strings.Contains(err.Error(), "conflicting rows") {
		t.Errorf("错误应说明多行冲突: %v", err)
	}
}

// 相同重复行 = 幂等行集 → 保持 unknown（两分支），不失败。
func TestT14ScalarDuplicateRowsUnknown(t *testing.T) {
	src := `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

func Config(ctx context.Context) (int, error) {
	var enabled int
	err := DB.QueryRowContext(ctx, "SELECT enabled FROM review_config WHERE region = ?", "ID").Scan(&enabled)
	return enabled, err
}

func Manual() {}
func Auto()   {}

func Entry(ctx context.Context) {
	enabled, err := Config(ctx)
	if err != nil {
		return
	}
	if enabled == 1 {
		Manual()
	} else {
		Auto()
	}
}
`
	snapBody := strings.Replace(t14bRuntime, `"scope":{"region":"ID","node_type":2}`,
		`"scope":{"region":"ID"}`, 1)
	snapBody = strings.Replace(snapBody, `"rows":[{"region":"ID","node_type":2,"enabled":1}]`,
		`"rows":[{"region":"ID","node_type":2,"enabled":1},{"region":"ID","node_type":2,"enabled":1}]`, 1)
	acts, dir, _ := seedT14b(t, src)
	snap := filepath.Join(dir, "runtime.json")
	_ = os.WriteFile(snap, []byte(snapBody), 0o644)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 6, RuntimeDataPath: snap})
	if err != nil {
		t.Fatalf("相同重复行不应失败: %v", err)
	}
	if len(seqCallNodesNamed(root, "Manual")) != 1 || len(seqCallNodesNamed(root, "Auto")) != 1 {
		t.Errorf("相同重复行应保持 unknown（两分支）: %v", labelsOf(root.Nodes))
	}
}

// 未证明语义的 SQL 形态（别名/限定名/表别名/INTO/hint）→ 不套值。
func TestT14UnsupportedSQLFormsNoValue(t *testing.T) {
	forms := map[string]string{
		"column-alias":  "SELECT enabled AS e FROM review_config WHERE region = ?",
		"qualified-col": "SELECT review_config.enabled FROM review_config WHERE region = ?",
		"table-alias":   "SELECT enabled FROM review_config r WHERE region = ?",
		"window":        "SELECT enabled FROM review_config WHERE region = ?",
		"hint":          "SELECT /*+ MAX_EXECUTION_TIME(1) */ enabled FROM review_config WHERE region = ?",
	}
	for name, sql := range forms {
		t.Run(name, func(t *testing.T) {
			src := `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

func Config(ctx context.Context) (int, error) {
	var enabled int
	err := DB.QueryRowContext(ctx, "` + sql + `", "ID").Scan(&enabled)
	return enabled, err
}

func Manual() {}
func Auto()   {}

func Entry(ctx context.Context) {
	enabled, err := Config(ctx)
	if err != nil {
		return
	}
	if enabled == 1 {
		Manual()
	} else {
		Auto()
	}
}
`
			acts, dir, snap := seedT14b(t, src)
			root, err := acts.CodeSequence(CodeSequenceRequest{
				Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 6, RuntimeDataPath: snap})
			if err != nil {
				t.Fatalf("形态拒绝不应让查询失败: %v", err)
			}
			if len(seqCallNodesNamed(root, "Manual")) != 1 || len(seqCallNodesNamed(root, "Auto")) != 1 {
				t.Errorf("%s 不应套用快照值（应两分支）: %v", name, labelsOf(root.Nodes))
			}
		})
	}
}

// *sql.DB 声明证明：自定义类型/局部同名变量不借用数据库绑定。
func TestT14DBBindingRequiresDeclaration(t *testing.T) {
	src := `package flow

import (
	"context"

	"example.com/flow/fake"
)

var DB fake.Handle

func Config(ctx context.Context) (int, error) {
	var n int
	// fake.Handle 不是 *sql.DB —— 不得当作数据库绑定
	err := DB.QueryRowContext(ctx, "SELECT enabled FROM review_config WHERE region = ?", "ID").Scan(&n)
	return n, err
}

func Manual() {}
func Auto()   {}

func Entry(ctx context.Context) {
	n, err := Config(ctx)
	if err != nil {
		return
	}
	if n == 1 {
		Manual()
	} else {
		Auto()
	}
}
`
	acts, dir, snap := seedT14b(t, src)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 6, RuntimeDataPath: snap})
	if err != nil {
		t.Fatal(err)
	}
	if len(seqCallNodesNamed(root, "Manual")) != 1 || len(seqCallNodesNamed(root, "Auto")) != 1 {
		t.Errorf("非 *sql.DB 变量不得借用绑定的快照值: %v", labelsOf(root.Nodes))
	}
}
