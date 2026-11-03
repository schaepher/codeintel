package action

// U04b：SQL 写入的结构化证据（action 层统一用 SQL AST 输出，不再按逗号/
// 字符串切分猜表列）。
//
// 边界（刻意保守）：
//   - 只认**已证明的 database/sql 写入调用**（U04a 的准确方法身份）；
//   - 只解析明确单表 + 显式列 + 单行 INSERT、UPDATE SET + WHERE AND 等式；
//   - 复杂/动态 SQL 不保留"看似确定"的表列，一律给边界代码；
//   - storage 只描述**语句结构**与实参绑定：值来源、资源身份、写入成功与
//     跨入口顺序都未证明（outcome 恒为 unproven）。

import (
	"go/ast"
	"go/token"
	"strings"

	"vitess.io/vitess/go/vt/sqlparser"
)

// 写入操作。
const (
	SeqStorageInsert = "insert"
	SeqStorageUpdate = "update"
)

// 值来源类别（U04b；U04c 追加 origin）。
const (
	SeqStorageValueArgument = "argument"
	SeqStorageValueLiteral  = "literal"
)

// 边界代码（缺证明的准确缺口）。
const (
	SeqStorageBoundaryRecord   = "record_identity_unproven"   // 记录/行身份未证明
	SeqStorageBoundaryResource = "resource_identity_unproven" // 资源（DB 句柄）身份未证明
	SeqStorageBoundaryShape    = "sql_shape_unproven"         // SQL 形态超出可证明范围
	SeqStorageBoundaryQuery    = "query_not_proven"           // 查询文本未证明（动态/不可解析）
	SeqStorageBoundaryArgs     = "argument_count_unproven"    // 占位符与实参数量不符
)

// seqPersistCtx 持久化注释所需的调用点上下文（源码/文件/包）。
type seqPersistCtx struct {
	file    *ast.File
	pkgPath string
	src     []byte
	fset    *token.FileSet
	guard   string
	// argOrigins U04c：调用实参的标量来源（按 call.Args 下标；SQL 之后的值
	// 实参用 [sqlArg+1:] 对齐 argument 编号）。
	argOrigins []*SeqStorageOrigin
	// runtime U04d：运行时快照分析（写入资源绑定结论）。
	runtime *runtimeAnalysis
}

// seqExprText 表达式在源码中的原文（缺失上下文返回空）。
func (c *seqPersistCtx) exprText(e ast.Expr) string {
	if c == nil || e == nil || c.fset == nil || len(c.src) == 0 {
		return ""
	}
	start := c.fset.Position(e.Pos()).Offset
	end := c.fset.Position(e.End()).Offset
	if start < 0 || end > len(c.src) || start >= end {
		return ""
	}
	return strings.TrimSpace(string(c.src[start:end]))
}

// seqSQLBinder 按占位符出现顺序绑定实参（不含 context/SQL）。
type seqSQLBinder struct {
	args    []ast.Expr
	origins []*SeqStorageOrigin
	used    int
	ctx     *seqPersistCtx
	// lenient 实参不足时仍产出结构（只用于诊断文本，不用于证据）。
	lenient bool
}

func (b *seqSQLBinder) bind(e sqlparser.Expr) (SeqStorageValue, string) {
	switch e.(type) {
	case *sqlparser.Argument:
		if b.used >= len(b.args) {
			if !b.lenient {
				return SeqStorageValue{}, SeqStorageBoundaryArgs
			}
			idx := b.used
			b.used++
			return SeqStorageValue{Kind: SeqStorageValueArgument, Argument: idx}, ""
		}
		idx := b.used
		b.used++
		return SeqStorageValue{Kind: SeqStorageValueArgument, Argument: idx,
			Expression: b.ctx.exprText(b.args[idx]), Origin: b.originAt(idx)}, ""
	}
	if text, ok := seqStorageLiteralText(e); ok {
		return SeqStorageValue{Kind: SeqStorageValueLiteral, Argument: -1, SQL: text}, ""
	}
	// 函数调用/表达式赋值：SQL 形态未证明（不猜值）。
	return SeqStorageValue{}, SeqStorageBoundaryShape
}

// originAt 第 idx 个值实参的标量来源（无则省略）。
func (b *seqSQLBinder) originAt(idx int) *SeqStorageOrigin {
	if b == nil || idx < 0 || idx >= len(b.origins) {
		return nil
	}
	return cloneSeqScalarOrigin(b.origins[idx])
}

// done 校验占位符与实参数量一致（诊断文本模式跳过）。
func (b *seqSQLBinder) done() string {
	if b.lenient {
		return ""
	}
	if b.used != len(b.args) {
		return SeqStorageBoundaryArgs
	}
	return ""
}

// seqStorageLiteralText SQL 字面量原文（字符串带引号）。
func seqStorageLiteralText(e sqlparser.Expr) (string, bool) {
	switch v := e.(type) {
	case *sqlparser.Literal:
		switch v.Type {
		case sqlparser.StrVal:
			return "'" + strings.ReplaceAll(v.Val, "'", "''") + "'", true
		case sqlparser.IntVal, sqlparser.FloatVal, sqlparser.DecimalVal,
			sqlparser.HexNum, sqlparser.HexVal:
			return v.Val, true
		}
		return "", false
	case *sqlparser.NullVal:
		return "null", true
	case sqlparser.BoolVal:
		if bool(v) {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// seqStorageTable 单个无别名表名（多表/别名/限定名/派生表都未证明）。
func seqStorageTable(ate *sqlparser.AliasedTableExpr) (string, bool) {
	if ate == nil || ate.As.String() != "" || len(ate.Hints) > 0 ||
		len(ate.Partitions) > 0 || len(ate.Columns) > 0 {
		return "", false
	}
	tn, ok := ate.Expr.(sqlparser.TableName)
	if !ok || tn.Name.String() == "" || tn.Qualifier.String() != "" {
		return "", false
	}
	return tn.Name.String(), true
}

// seqStorageColumns 显式列名（无重复、无限定名）。
func seqStorageColumns(cols sqlparser.Columns) ([]string, bool) {
	if len(cols) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(cols))
	seen := map[string]bool{}
	for _, c := range cols {
		name := c.String()
		if name == "" || seen[strings.ToLower(name)] {
			return nil, false
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	return out, true
}

// seqStorageResourceDecl 准确的包级无初始化 `var X *sql.DB` 声明符号
// （局部同名/已初始化/其它类型都不借身份）。
func seqStorageResourceDecl(call *ast.CallExpr, c *seqPersistCtx) string {
	if call == nil || c == nil || c.file == nil || c.pkgPath == "" {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name == "" || id.Obj == nil || id.Obj.Kind != ast.Var {
		return ""
	}
	vs, ok := id.Obj.Decl.(*ast.ValueSpec)
	if !ok || !seqFileLevelVarSpec(c.file, vs) {
		return ""
	}
	if !runtimeIsSQLDBDecl(id, c.file) {
		return ""
	}
	return "symbol:go:" + c.pkgPath + ":" + id.Name
}

// seqFileLevelVarSpec 该 ValueSpec 是否为文件级（包级）var 声明。
func seqFileLevelVarSpec(f *ast.File, vs *ast.ValueSpec) bool {
	if f == nil || vs == nil {
		return false
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			if spec == vs {
				return true
			}
		}
	}
	return false
}

// seqStorageEvidence 给已证明的写入调用构造结构化 storage（含资源身份与
// 缺口边界）。node 的 Evidence 必须已写入准确目标 ID。
func seqStorageEvidence(node *CodeSeqNode, call *ast.CallExpr, c *seqPersistCtx) *SeqStorage {
	sqlArg := seqPersistenceSQLArg(node)
	if sqlArg < 0 || call == nil {
		return nil
	}
	if sqlArg >= len(call.Args) {
		return &SeqStorage{Outcome: "unproven", Boundary: SeqStorageBoundaryQuery}
	}
	sql := seqPersistenceSQLText(call, sqlArg)
	if sql == "" {
		return &SeqStorage{Outcome: "unproven", Boundary: SeqStorageBoundaryQuery}
	}
	valueArgs := call.Args[sqlArg+1:]
	if call.Ellipsis.IsValid() && len(valueArgs) > 0 {
		return &SeqStorage{Outcome: "unproven", Boundary: SeqStorageBoundaryArgs}
	}
	binder := &seqSQLBinder{args: valueArgs, ctx: c}
	if c != nil && len(c.argOrigins) > sqlArg+1 {
		binder.origins = c.argOrigins[sqlArg+1:]
	}
	st, code := seqStorageParse(sql, binder)
	if st == nil {
		return &SeqStorage{Outcome: "unproven", Boundary: code}
	}
	resource := seqStorageResourceDecl(call, c)
	if resource != "" {
		st.ResourceDeclaration = resource
		st.Boundary = SeqStorageBoundaryRecord
	} else {
		st.Boundary = SeqStorageBoundaryResource
	}
	if c != nil && c.guard != "" {
		st.Guard = c.guard
	}
	return st
}

// seqAnnotateCallEvidence 调用节点的收尾证据：运行时读取、调用证据
// 收口、持久化边界诊断（U04）与结构化写入证据（U04b/U04c）。
func seqAnnotateCallEvidence(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr,
	f *ast.File, fset *token.FileSet, src []byte, curPkg string) {
	attachRuntimeEvidence(req, node, call)
	finishSeqEvidence(req, node)
	// 值来源/跨入口顺序仍需请求证明，不据此凭空连线。
	seqAnnotatePersistence(node, call)
	seqAttachStorageEvidence(node, call, &seqPersistCtx{file: f, pkgPath: curPkg,
		src: src, fset: fset, guard: req.branchCond, argOrigins: req.callArgOrigins,
		runtime: req.runtime})
}

// seqAttachStorageEvidence 把结构化写入证据写到调用节点（不覆盖既有
// 证据字段）。
func seqAttachStorageEvidence(node *CodeSeqNode, call *ast.CallExpr, c *seqPersistCtx) {
	if node == nil || node.Evidence == nil {
		return
	}
	st := seqStorageEvidence(node, call, c)
	if st == nil {
		return
	}
	// U04d：写入资源绑定（无快照省略；未访问/冲突如实标注）。
	if c != nil && c.runtime != nil {
		if res := c.runtime.writeResource(call); res != nil {
			cp := *res
			st.Resource = &cp
		} else {
			st.Resource = &SeqStorageResource{Status: SeqResourceNotVisited,
				SnapshotHash: c.runtime.snap.Hash}
		}
	}
	node.Evidence.Storage = st
}
