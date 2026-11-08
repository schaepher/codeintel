package action

// 资源遍历：switch/select 分支体（与块相同的可达性规则 + 分支终止边界）。

import "go/ast"

// walkCaseBody switch/select 分支体（与块相同的可达性规则 + 分支终止边界）。
func (b *seqResourceFactBuilder) walkCaseBody(stmts []ast.Stmt, clause ast.Node,
	file *seqParsedGoFile, eventAt int, isEntry bool, depth int,
	closures map[*ast.Object]*ast.FuncLit) {
	mark := len(b.log)
	for _, st := range stmts {
		b.walkStmt(st, file, eventAt, isEntry, depth, closures)
		if seqStmtStopsBlock(st) {
			break
		}
	}
	if isEntry && file.fset != nil && seqBlockTerminates(&ast.BlockStmt{List: stmts}) &&
		len(stmts) > 0 {
		die := file.fset.Position(stmts[len(stmts)-1].End()).Offset
		if die <= 0 {
			die = file.fset.Position(clause.End()).Offset
		}
		for _, ref := range b.log[mark:] {
			ev := &b.out.events[ref.name][ref.idx]
			if ev.die == 0 || die < ev.die {
				ev.die = die
			}
		}
	}
}
