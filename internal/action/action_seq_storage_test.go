package action

// U04b：SQL 写入的结构化证据（AST 解析 + 实参绑定 + 资源身份边界）。
// 反例矩阵覆盖：多行/INSERT SELECT/IGNORE/REPLACE/ON DUP/重复列/别名/
// 限定名/多表/表达式赋值/OR/IN/LIMIT/命名绑定/多语句。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// seqStorageFixture 解析一段调用点源码，返回（节点、调用、上下文）。
func seqStorageFixture(t *testing.T, src, want string) (*CodeSeqNode, *ast.CallExpr, *seqPersistCtx) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "flow.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var call *ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || call != nil {
			return call == nil
		}
		start := fset.Position(c.Pos()).Offset
		end := fset.Position(c.End()).Offset
		if strings.HasPrefix(string(src[start:end]), want) {
			call = c
		}
		return call == nil
	})
	if call == nil {
		t.Fatalf("找不到调用 %q", want)
	}
	node := &CodeSeqNode{Kind: "call",
		Evidence: &SeqEvidence{Source: SeqEvidenceCallEdge, SymbolID: SeqEvidenceSymbolForCall(call)}}
	return node, call, &seqPersistCtx{file: f, pkgPath: "example.com/storageflow", src: []byte(src), fset: fset}
}

// SeqEvidenceSymbolForCall 测试辅助：按方法名映射到标准库写入身份。
func SeqEvidenceSymbolForCall(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	switch sel.Sel.Name {
	case "Exec":
		return seqSQLMethodExec
	case "ExecContext":
		return seqSQLMethodDBExecContext
	}
	return ""
}

const storageSrc = `package storageflow
import "database/sql"
var DB *sql.DB
func Save(id string) {
	DB.Exec("UPDATE nodes SET kind=? WHERE id=? AND region='ID'", "service", id)
}
`

// TestStorageUpdateArguments UPDATE：SET 占位符在 WHERE 之前按顺序编号。
func TestStorageUpdateArguments(t *testing.T) {
	node, call, c := seqStorageFixture(t, storageSrc, "DB.Exec(")
	st := seqStorageEvidence(node, call, c)
	if st == nil {
		t.Fatal("expected storage evidence")
	}
	if st.Operation != SeqStorageUpdate || st.Table != "nodes" {
		t.Fatalf("unexpected storage: %+v", st)
	}
	if len(st.Writes) != 1 || st.Writes[0].Column != "kind" ||
		st.Writes[0].Value.Kind != SeqStorageValueArgument || st.Writes[0].Value.Argument != 0 ||
		st.Writes[0].Value.Expression != `"service"` {
		t.Fatalf("writes: %+v", st.Writes)
	}
	if len(st.Predicates) != 2 {
		t.Fatalf("predicates: %+v", st.Predicates)
	}
	if st.Predicates[0].Column != "id" || st.Predicates[0].Value.Argument != 1 ||
		st.Predicates[0].Value.Expression != "id" {
		t.Fatalf("predicate[0]: %+v", st.Predicates[0])
	}
	if st.Predicates[1].Column != "region" || st.Predicates[1].Value.Kind != SeqStorageValueLiteral ||
		st.Predicates[1].Value.Argument != -1 || st.Predicates[1].Value.SQL != "'ID'" {
		t.Fatalf("predicate[1]: %+v", st.Predicates[1])
	}
	if st.ResourceDeclaration != "symbol:go:example.com/storageflow:DB" {
		t.Fatalf("resource_declaration: %q", st.ResourceDeclaration)
	}
	if st.Outcome != "unproven" || st.Boundary != SeqStorageBoundaryRecord {
		t.Fatalf("outcome/boundary: %+v", st)
	}
}

// TestStorageInsertCommasInsideString SQL 字符串里的逗号不切列。
func TestStorageInsertCommasInsideString(t *testing.T) {
	src := `package storageflow
import "database/sql"
var DB *sql.DB
func Save() { DB.Exec("INSERT INTO nodes(id,kind) VALUES(?,?)", "a,b", "c") }
`
	node, call, c := seqStorageFixture(t, src, "DB.Exec(")
	st := seqStorageEvidence(node, call, c)
	if st == nil || len(st.Writes) != 2 {
		t.Fatalf("writes: %+v", st)
	}
	if st.Writes[0].Column != "id" || st.Writes[1].Column != "kind" {
		t.Fatalf("columns: %+v", st.Writes)
	}
	if st.Writes[0].Value.Expression != `"a,b"` {
		t.Fatalf("expression: %q", st.Writes[0].Value.Expression)
	}
}

// TestStorageLocalResource 局部 DB 参数：无 resource_declaration + 资源边界。
func TestStorageLocalResource(t *testing.T) {
	src := `package storageflow
import "database/sql"
func Save(db *sql.DB) { db.Exec("INSERT INTO nodes(id) VALUES(?)", "n") }
`
	node, call, c := seqStorageFixture(t, src, "db.Exec(")
	st := seqStorageEvidence(node, call, c)
	if st == nil || st.Table != "nodes" {
		t.Fatalf("storage: %+v", st)
	}
	if st.ResourceDeclaration != "" {
		t.Fatalf("局部 DB 不得借包级声明: %q", st.ResourceDeclaration)
	}
	if st.Boundary != SeqStorageBoundaryResource {
		t.Fatalf("boundary = %q", st.Boundary)
	}
}

// TestStorageShapeRejections 形态未证明：不输出表列。
func TestStorageShapeRejections(t *testing.T) {
	cases := []string{
		`INSERT INTO nodes(id) SELECT id FROM other`,
		`INSERT INTO nodes(id) VALUES(?), (?)`,
		`INSERT IGNORE INTO nodes(id) VALUES(?)`,
		`REPLACE INTO nodes(id) VALUES(?)`,
		`INSERT INTO nodes(id) VALUES(?) ON DUPLICATE KEY UPDATE id=?`,
		`INSERT INTO nodes(id,id) VALUES(?,?)`,
		`INSERT INTO t AS x (id) VALUES(?)`,
		`INSERT INTO db.nodes(id) VALUES(?)`,
		`UPDATE nodes SET kind=(SELECT kind FROM other) WHERE id=?`,
		`UPDATE nodes SET kind=? WHERE id=? OR region=?`,
		`UPDATE nodes SET kind=? WHERE id IN (?)`,
		`UPDATE nodes SET kind=? WHERE id=? LIMIT 1`,
		`UPDATE nodes SET kind=? WHERE id=?; DROP TABLE nodes`,
		`INSERT INTO nodes(id) VALUES(:id)`,
	}
	for _, sql := range cases {
		t.Run(sql, func(t *testing.T) {
			src := "package storageflow\nimport \"database/sql\"\nvar DB *sql.DB\nfunc Save() { DB.Exec(" +
				seqStorageQuoteSQL(sql) + `, "a", "b", "c") }` + "\n"
			node, call, c := seqStorageFixture(t, src, "DB.Exec(")
			st := seqStorageEvidence(node, call, c)
			if st == nil {
				t.Fatal("expected boundary storage")
			}
			if st.Table != "" || len(st.Writes) != 0 || len(st.Predicates) != 0 {
				t.Fatalf("不应保留部分表列: %+v", st)
			}
			if st.Boundary != SeqStorageBoundaryShape && st.Boundary != SeqStorageBoundaryQuery {
				t.Fatalf("boundary = %q", st.Boundary)
			}
		})
	}
}

// TestStorageArgumentCount 占位符与实参数量不符。
func TestStorageArgumentCount(t *testing.T) {
	src := `package storageflow
import "database/sql"
var DB *sql.DB
func Save() { DB.Exec("INSERT INTO nodes(id,kind) VALUES(?,?)", "only") }
`
	node, call, c := seqStorageFixture(t, src, "DB.Exec(")
	st := seqStorageEvidence(node, call, c)
	if st == nil || st.Boundary != SeqStorageBoundaryArgs {
		t.Fatalf("storage: %+v", st)
	}
	if st.Table != "" || len(st.Writes) != 0 {
		t.Fatalf("数量不符不得保留部分表列: %+v", st)
	}
}

// TestStorageDynamicQuery 动态 SQL：query_not_proven，无表列。
func TestStorageDynamicQuery(t *testing.T) {
	src := `package storageflow
import "database/sql"
var DB *sql.DB
func Save(query string) { DB.Exec(query, "INSERT INTO nodes(id) VALUES(?)") }
`
	node, call, c := seqStorageFixture(t, src, "DB.Exec(")
	st := seqStorageEvidence(node, call, c)
	if st == nil || st.Boundary != SeqStorageBoundaryQuery || st.Table != "" {
		t.Fatalf("storage: %+v", st)
	}
}

func seqStorageQuoteSQL(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
