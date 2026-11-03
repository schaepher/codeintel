package action

// U04y：字段赋值观察（field_writes, assignment_only）与准确求值顺序。
//
// 只观察"发生了这次赋值"：value 仅为右操作数来源，不是运算后的字段值；
// 不推导生成键、当前可变字段值、列映射、提交成功或跨入口同一记录。

import (
	"go/ast"
	"go/token"
)

// 常量与预算。
const (
	SeqFieldWriteSemantics   = "assignment_only"
	SeqStopFieldWriteBudget  = "field_write_budget"
	seqFieldWriteMaxObserved = 256
)

// SeqFieldWriteTarget 赋值目标（声明身份 + 可选对象根）。
type SeqFieldWriteTarget struct {
	TypeID     string        `json:"type_id,omitempty"`
	SourceFile string        `json:"source_file,omitempty"`
	SourceLine int           `json:"source_line,omitempty"`
	Path       []string      `json:"path"`
	Root       *SeqFieldRoot `json:"root,omitempty"`
}

// SeqFieldWrite 字段赋值观察。
type SeqFieldWrite struct {
	ID         string              `json:"id"`
	Semantics  string              `json:"semantics"`
	File       string              `json:"file,omitempty"`
	Offset     int                 `json:"offset,omitempty"`
	Operator   string              `json:"operator"`
	Target     SeqFieldWriteTarget `json:"target"`
	Value      *SeqStorageOrigin   `json:"value,omitempty"`
	Expression string              `json:"expression,omitempty"`
	ResultSlot int                 `json:"result_slot"`
	Guard      string              `json:"guard,omitempty"`
	Scheduled  bool                `json:"scheduled,omitempty"`
}

// seqNextFieldWriteID 赋值观察 ID（超预算 → 空）。
func seqNextFieldWriteID(req CodeSequenceRequest) string {
	if req.acc == nil || req.acc.fieldWriteSeq >= seqFieldWriteMaxObserved {
		return ""
	}
	req.acc.fieldWriteSeq++
	return "field-write-" + itoaSeq(req.acc.fieldWriteSeq)
}

// seqFieldWriteNodes 赋值语句里的字段写入 → assign 节点（默认时序保留）。
func seqFieldWriteNodes(req CodeSequenceRequest, fset *token.FileSet, src []byte,
	st ast.Stmt) []*CodeSeqNode {
	var lhsList []ast.Expr
	var rhsList []ast.Expr
	var tok token.Token
	var pos token.Pos
	switch s := st.(type) {
	case *ast.AssignStmt:
		lhsList, rhsList, tok, pos = s.Lhs, s.Rhs, s.Tok, s.Pos()
	case *ast.IncDecStmt:
		lhsList, tok, pos = []ast.Expr{s.X}, s.Tok, s.Pos()
	default:
		return nil
	}
	var out []*CodeSeqNode
	for i, lhs := range lhsList {
		write, ok := seqFieldWriteFor(req, lhs, rhsList, i, tok)
		if !ok {
			continue
		}
		var rhs ast.Expr
		if i < len(rhsList) {
			rhs = rhsList[i]
		}
		write.Expression = seqExprText(fset, src, rhs)
		node := &CodeSeqNode{Kind: "assign",
			Label: assignmentLabel(fset, src, lhs, tok, rhs), Line: fset.Position(pos).Line}
		if write.ID == "" {
			node.Stop = &SeqStop{Code: SeqStopFieldWriteBudget,
				Detail: "field assignment observation budget exhausted (" +
					itoaSeq(seqFieldWriteMaxObserved) + "); this assignment is not partially represented"}
		} else {
			node.FieldWrites = []SeqFieldWrite{write}
		}
		out = append(out, node)
	}
	return out
}

// assignmentLabel `r.ID = id` / `r.ID += id` / `r.ID++`。
func assignmentLabel(fset *token.FileSet, src []byte, lhs ast.Expr, tok token.Token,
	rhs ast.Expr) string {
	l := seqExprText(fset, src, lhs)
	switch tok {
	case token.INC, token.DEC:
		return l + tok.String()
	case token.ASSIGN:
		return l + " = " + seqExprText(fset, src, rhs)
	}
	return l + " " + tok.String() + " " + seqExprText(fset, src, rhs)
}

// seqFieldWriteFor 单个 LHS 的字段写入观察（非字段目标返回 false）。
func seqFieldWriteFor(req CodeSequenceRequest, lhs ast.Expr, rhsList []ast.Expr, idx int,
	tok token.Token) (SeqFieldWrite, bool) {
	sel, ok := runtimeUnparen(lhs).(*ast.SelectorExpr)
	if !ok {
		return SeqFieldWrite{}, false
	}
	write := SeqFieldWrite{Semantics: SeqFieldWriteSemantics, Operator: tok.String(),
		Guard: req.branchCond, Scheduled: req.scheduled,
		ResultSlot: seqResultSlotIndex(rhsList, idx)}
	if req.curCtx != nil {
		write.File = req.curCtx.rel
		write.Offset = req.curCtx.fset.Position(lhs.Pos()).Offset
	}
	// 目标声明身份 + 可选对象根（实例来源未证明时省略 root）。
	target, root, ok := seqFieldWriteTarget(req, sel)
	if !ok {
		return SeqFieldWrite{}, false
	}
	write.Target = target
	write.Target.Root = root
	// value：只为右操作数来源（compound 也不把右操作数当运算后值）。
	if idx < len(rhsList) {
		write.Value = seqScalarOriginOf(req, rhsList[idx])
	} else if len(rhsList) == 1 {
		write.Value = seqResultSlotOrigin(req, rhsList[0], idx)
	}
	id := seqNextFieldWriteID(req)
	write.ID = id
	return write, true
}

// seqResultSlotIndex 多返回值/多重赋值的槽位。
func seqResultSlotIndex(rhs []ast.Expr, idx int) int {
	if len(rhs) == 1 && idx > 0 {
		if _, ok := rhs[0].(*ast.CallExpr); ok {
			return idx
		}
	}
	return 0
}

// seqResultSlotOrigin RHS 单调用多槽 → 指定槽来源。
func seqResultSlotOrigin(req CodeSequenceRequest, rhs ast.Expr, slot int) *SeqStorageOrigin {
	if call, ok := rhs.(*ast.CallExpr); ok {
		return seqNodeResultOriginAt(req, call, slot)
	}
	return nil
}

// seqFieldWriteTarget 目标字段的声明（owner/path/声明位置）与对象根。
func seqFieldWriteTarget(req CodeSequenceRequest, sel *ast.SelectorExpr) (SeqFieldWriteTarget,
	*SeqFieldRoot, bool) {
	base := seqFieldBaseOrigin(req, sel.X)
	if base == nil {
		// 实例来源未知：仍可给出声明（按静态声明类型）。
		if id, ok := sel.X.(*ast.Ident); ok && id.Obj != nil {
			if ref, ok2 := seqIdentStructType(req, id); ok2 {
				if field, ok3 := seqResolveFieldDecl(req, ref, sel.Sel.Name); ok3 {
					return SeqFieldWriteTarget{TypeID: "symbol:go:" + field.ownerpkg + ":" + field.owner,
						SourceFile: field.sourceFile, SourceLine: field.sourceLine,
						Path: []string{sel.Sel.Name}}, nil, true
				}
			}
		}
		return SeqFieldWriteTarget{}, nil, false
	}
	field, ok := seqResolveFieldDecl(req, base.ref, sel.Sel.Name)
	if !ok {
		return SeqFieldWriteTarget{}, nil, false
	}
	path := append(append([]string{}, base.path...), sel.Sel.Name)
	return SeqFieldWriteTarget{TypeID: "symbol:go:" + field.ownerpkg + ":" + field.owner,
			SourceFile: field.sourceFile, SourceLine: field.sourceLine, Path: path},
		cloneSeqFieldRoot(base.root), true
}
