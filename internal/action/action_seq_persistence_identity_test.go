package action

// U04a：持久化诊断必须属于**实际数据库写入调用**——只有 database/sql 的
// DB/Tx Exec、DB/Tx/Conn ExecContext 准确方法身份才解析 SQL；自定义同名
// 方法、日志调用、Stmt.Exec 都不是写入。

import (
	"go/ast"
	"go/parser"
	"testing"
)

// seqPersistCall 解析一个调用表达式。
func seqPersistCall(t *testing.T, expr string) *ast.CallExpr {
	t.Helper()
	e, err := parser.ParseExpr(expr)
	if err != nil {
		t.Fatal(err)
	}
	call, ok := e.(*ast.CallExpr)
	if !ok {
		t.Fatalf("不是调用表达式: %s", expr)
	}
	return call
}

// TestPersistenceLogIsNotWrite E-U04a 回归：日志调用的 SQL 字面量不是写入。
func TestPersistenceLogIsNotWrite(t *testing.T) {
	expr := seqPersistCall(t, `Log("INSERT INTO nodes(id) VALUES (?)", id)`)
	n := &CodeSeqNode{Evidence: &SeqEvidence{SymbolID: "symbol:go:example.com/flow:Log"}}
	seqAnnotatePersistence(n, expr)
	if n.Stop != nil {
		t.Fatalf("unexpected persistence: %+v", n.Stop)
	}
}

// TestPersistenceWriteIdentityMatrix 准确写入方法保留诊断；其余形态不得产生写入诊断。
func TestPersistenceWriteIdentityMatrix(t *testing.T) {
	cases := []struct {
		name   string
		expr   string
		symbol string
		want   bool
	}{
		{"DB.Exec", `DB.Exec("INSERT INTO nodes(id) VALUES (?)", id)`,
			"symbol:go:database/sql:(DB).Exec", true},
		{"Tx.Exec", `tx.Exec("UPDATE nodes SET kind=? WHERE id=?", kind, id)`,
			"symbol:go:database/sql:(Tx).Exec", true},
		{"DB.ExecContext", `DB.ExecContext(ctx, "INSERT INTO nodes(id) VALUES (?)", id)`,
			"symbol:go:database/sql:(DB).ExecContext", true},
		{"Conn.ExecContext", `conn.ExecContext(ctx, "INSERT INTO nodes(id) VALUES (?)", id)`,
			"symbol:go:database/sql:(Conn).ExecContext", true},
		// 自定义同名方法：不是 database/sql 身份。
		{"custom DB.Exec", `DB.Exec("INSERT INTO nodes(id) VALUES (?)", id)`,
			"symbol:go:example.com/flow:(DB).Exec", false},
		// 缺 SymbolID（未解析调用）。
		{"missing symbol", `DB.Exec("INSERT INTO nodes(id) VALUES (?)", id)`, "", false},
		// Stmt.Exec：SQL 来自 Prepare，值实参不是查询。
		{"Stmt.Exec", `stmt.Exec("INSERT INTO nodes(id) VALUES (?)")`,
			"symbol:go:database/sql:(Stmt).Exec", false},
		// 查询不是 SQL 位置（Exec 参数 0 / ExecContext 参数 1）。
		{"Exec query variable", `DB.Exec(query, "INSERT INTO nodes(id) VALUES (?)")`,
			"symbol:go:database/sql:(DB).Exec", false},
		{"ExecContext sql in slot 0", `DB.ExecContext("INSERT INTO nodes(id) VALUES (?)", query)`,
			"symbol:go:database/sql:(DB).ExecContext", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := &CodeSeqNode{Evidence: &SeqEvidence{SymbolID: c.symbol}}
			seqAnnotatePersistence(n, seqPersistCall(t, c.expr))
			if got := n.Stop != nil; got != c.want {
				t.Fatalf("persistence stop = %v (want %v): %+v", got, c.want, n.Stop)
			}
			if c.want && n.Stop.Code != SeqStopIndexGap {
				t.Fatalf("persistence stop code = %q", n.Stop.Code)
			}
		})
	}
}

// TestPersistenceSQLUnquote SQL 字符串按 Go 语法解码（转义不残留）。
func TestPersistenceSQLUnquote(t *testing.T) {
	n := &CodeSeqNode{Evidence: &SeqEvidence{SymbolID: "symbol:go:database/sql:(DB).Exec"}}
	seqAnnotatePersistence(n, seqPersistCall(t, "DB.Exec(\"INSERT INTO nodes(id) VALUES(?)\", id)"))
	if n.Stop == nil || n.Stop.Detail == "" {
		t.Fatalf("expected persistence detail, got %+v", n.Stop)
	}
}
