package action

// B03：ORM 模型/表/列 → 工作流写入/读取（从 action_seq_workflow_conn.go 拆出）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// B03：ORM（xorm 风格）模型 → 表列映射（显式 Table + 准确列标签；无标签
// 用 snake_case 默认策略，动态命名/链对象重绑不接入）。
const seqORMTableNameMaxFiles = 64

func seqORMColumnName(tag, field string) string {
	if tag != "" {
		if i := strings.Index(tag, ":"); i >= 0 {
			tag = tag[i+1:]
		}
		tag = strings.Trim(tag, `"' `)
		// B03e.4：xorm 的 `-` 表示字段不映射数据库列，不能回退默认列名。
		if tag == "-" {
			return ""
		}
		// xorm 常见：`pk autoincr 'id'` / `'kind'` / `column:id`。
		if i := strings.IndexAny(tag, "'\""); i >= 0 {
			q := tag[i]
			if j := strings.IndexByte(tag[i+1:], q); j >= 0 {
				if col := strings.TrimSpace(tag[i+1 : i+1+j]); col != "" {
					return col
				}
			}
		}
		for _, tok := range strings.FieldsFunc(tag, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		}) {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			if strings.HasPrefix(tok, "column:") {
				return strings.TrimPrefix(tok, "column:")
			}
			low := strings.ToLower(tok)
			switch low {
			case "-":
				return "" // 忽略字段：没有数据库列来源。
			case "pk", "autoincr", "not", "null", "notnull", "unique", "index",
				"default", "->", "<-":
				continue
			}
			return tok
		}
	}
	return snakeCase(field)
}

// seqORMWritesFromEvidence ORM 写入证据 → 工作流写入。
func seqORMWritesFromEvidence(node *CodeSeqNode, entry, repo string) []seqWFWrite {
	orm := node.Evidence.ORM
	if orm == nil || (orm.Operation != SeqStorageInsert && orm.Operation != SeqStorageUpdate) ||
		orm.Table == "" || len(orm.Arguments) == 0 {
		return nil
	}
	var out []seqWFWrite
	w := seqWFWrite{Entry: entry, Repo: repo, File: node.Evidence.CallFile,
		Line: node.Evidence.CallLine, Table: orm.Table, Op: orm.Operation, Guard: orm.Guard,
		Preds: orm.Predicates, Resource: seqORMSessionResource(node)}
	w.ID = "orm-write@" + node.Evidence.CallFile + ":" + itoaSeq(node.Evidence.CallLine)
	for _, m := range orm.Models {
		if m.Argument < 0 || m.Argument >= len(orm.Arguments) {
			continue
		}
		sl := orm.Arguments[m.Argument].Origin
		if sl == nil || sl.Kind != SeqOriginStructLiteral || sl.StructLiteral == nil {
			continue
		}
		cols := seqORMColumns(m)
		for _, f := range sl.StructLiteral.Fields {
			col, ok := cols[f.Name]
			if !ok || f.Origin == nil {
				continue
			}
			w.Values = append(w.Values, SeqStorageWrite{Column: col,
				Value: SeqStorageValue{Kind: SeqStorageValueArgument, Origin: f.Origin}})
		}
	}
	if len(w.Values) == 0 {
		return nil
	}
	out = append(out, w)
	return out
}

// seqORMReadsFromEvidence ORM 读取证据 → 工作流读取。
func seqORMReadsFromEvidence(node *CodeSeqNode, entry, repo string) []seqWFRead {
	orm := node.Evidence.ORM
	if orm == nil || orm.Operation != "select" || orm.Table == "" || len(orm.Predicates) == 0 {
		return nil
	}
	var cols []string
	for _, m := range orm.Models {
		for _, b := range seqORMValidColumnBindings(m) {
			cols = append(cols, b.Column)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	return []seqWFRead{{Entry: entry, Repo: repo, File: node.Evidence.CallFile,
		Line: node.Evidence.CallLine, ReadID: "orm-read@" + node.Evidence.CallFile + ":" +
			itoaSeq(node.Evidence.CallLine), Table: orm.Table, Columns: cols,
		Preds: orm.Predicates, Resource: seqORMSessionResource(node), Guard: orm.Guard}}
}

// seqORMSessionResource ORM 调用的资源声明（框架证据不提供句柄符号 → 空；
// 两侧都空时连接带 resource_binding 缺失证明）。
func seqORMSessionResource(*CodeSeqNode) string { return "" }

// seqORMStaticModelTable 模型声明上的静态 TableName() → 表名。
//
// 身份按**模型类型的接收者声明**匹配（不是方法短名/目录扫描顺序）：同名
// 方法定义在其它类型上、动态拼接/变量返回、链重绑都返回 false（unknown）。
func seqORMStaticModelTable(c *seqPersistCtx, models []SeqORMModel) (string, bool) {
	if c == nil || c.req == nil || len(models) == 0 {
		return "", false
	}
	table := ""
	for _, model := range models {
		name := seqORMTypeNameOfSymbol(model.SymbolID)
		if name == "" {
			return "", false
		}
		pkg := seqORMPkgOfSymbol(model.SymbolID)
		got, ok := seqORMStaticTypeTableName(c, pkg, name)
		if !ok {
			return "", false
		}
		if table != "" && table != got {
			return "", false // 多个模型指向不同表 → 不猜。
		}
		table = got
	}
	return table, table != ""
}

// seqORMStaticTypeTableName 包内接收者类型恰为 name 的 TableName 方法。
func seqORMStaticTypeTableName(c *seqPersistCtx, pkg, name string) (string, bool) {
	scanned := 0
	table := ""
	found := 0
	for _, ctx := range seqPkgFilesForReq(*c.req, pkg, "") {
		if ctx == nil || ctx.file == nil || ctx.fset == nil || ctx.src == nil {
			continue
		}
		scanned++
		if scanned > seqORMTableNameMaxFiles {
			return "", false // 预算不足：不能猜默认表名。
		}
		for _, d := range ctx.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Name == nil || fd.Name.Name != "TableName" ||
				fd.Body == nil {
				continue
			}
			recv := seqORMRecvBaseName(fd.Recv)
			if recv != name {
				continue // 其它类型的同名方法不能借用身份。
			}
			found++
			if found > 1 {
				return "", false
			}
			lit, ok := seqORMStaticReturnString(fd.Body)
			if !ok {
				return "", false
			}
			table = lit
		}
	}
	if found != 1 {
		return "", false
	}
	return table, table != ""
}

// seqORMRecvBaseName 接收者类型名（去指针/泛型实例）。
func seqORMRecvBaseName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	typ := recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if idx, ok := typ.(*ast.IndexExpr); ok {
		typ = idx.X
	}
	if idx, ok := typ.(*ast.IndexListExpr); ok {
		typ = idx.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// seqORMStaticReturnString 方法体唯一 return 的字符串字面量。
func seqORMStaticReturnString(body *ast.BlockStmt) (string, bool) {
	if body == nil {
		return "", false
	}
	var rets []*ast.ReturnStmt
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			rets = append(rets, v)
			return false
		}
		return true
	})
	if len(rets) != 1 || len(rets[0].Results) != 1 {
		return "", false
	}
	lit, ok := rets[0].Results[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, ok := seqFrontendStringLit(lit.Value)
	if !ok || !seqXormSimpleIdent(s) {
		return "", false
	}
	return s, true
}

// seqORMTypeNameOfSymbol `symbol:go:<pkg>:<name>` → name。
func seqORMTypeNameOfSymbol(id string) string {
	i := strings.LastIndex(id, ":")
	if i < 0 || i+1 >= len(id) {
		return ""
	}
	return id[i+1:]
}

// seqORMPkgOfSymbol `symbol:go:<pkg>:<name>` → pkg。
func seqORMPkgOfSymbol(id string) string {
	const prefix = "symbol:go:"
	if !strings.HasPrefix(id, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(id, prefix)
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return ""
	}
	return rest[:i]
}

// B05b：ORM 自动主键声明（pk + autoincr 标签）。
func seqORMPKField(m SeqORMModel) (string, string, bool) {
	for _, f := range m.Fields {
		low := strings.ToLower(f.Tag)
		if !strings.Contains(low, "pk") || !strings.Contains(low, "autoincr") {
			continue
		}
		return f.Name, seqORMColumnName(f.Tag, f.Name), true
	}
	return "", "", false
}
