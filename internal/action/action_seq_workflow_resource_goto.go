package action

// U04bU.1：goto 的标签目标可达性。
//
// break/continue 只停止当前块；goto 必须沿函数级真实标签声明继续，否则
// 目标标签后的资源改写会被漏掉。目标按函数体声明身份解析（不跨函数/闭包
// 按短名匹配）；重复/向后跳转与预算超限 fail-closed（partial）。

import "go/ast"

const seqResourceGotoMaxHops = 64

// seqGotoTarget 标签所在语句列表与下标（goto 后从该位置继续执行）。
type seqGotoTarget struct {
	list []ast.Stmt
	idx  int
}

// seqGotoFrame 一个函数（含闭包）的标签表与已跟随目标。
type seqGotoFrame struct {
	labels  map[string]seqGotoTarget
	visited map[string]bool
}

// pushGotoFrame 收集函数体的标签声明（不进入嵌套函数字面量）。
func (b *seqResourceFactBuilder) pushGotoFrame(body *ast.BlockStmt) {
	f := seqGotoFrame{labels: map[string]seqGotoTarget{}, visited: map[string]bool{}}
	if body != nil {
		seqCollectGotoLabels(body.List, f.labels)
	}
	b.gotoFrames = append(b.gotoFrames, f)
}

// popGotoFrame 退出当前函数/闭包。
func (b *seqResourceFactBuilder) popGotoFrame() {
	if n := len(b.gotoFrames); n > 0 {
		b.gotoFrames = b.gotoFrames[:n-1]
	}
}

// walkGotoTarget 从 goto 的真实标签目标继续遍历到所在列表末尾。
func (b *seqResourceFactBuilder) walkGotoTarget(name string, file *seqParsedGoFile,
	eventAt int, isEntry bool, depth int, closures map[*ast.Object]*ast.FuncLit) {
	if len(b.gotoFrames) == 0 {
		b.out.partial = true
		return
	}
	fr := &b.gotoFrames[len(b.gotoFrames)-1]
	tgt, ok := fr.labels[name]
	if !ok {
		b.out.partial = true // 目标未访问/不存在：不能当作无改写。
		return
	}
	if fr.visited[name] || b.gotoHops >= seqResourceGotoMaxHops {
		b.out.partial = true // 向后/循环/重复目标：旧状态不能复用。
		return
	}
	fr.visited[name] = true
	b.gotoHops++
	for _, st := range tgt.list[tgt.idx:] {
		b.walkStmt(st, file, eventAt, isEntry, depth, closures)
		if seqStmtStopsBlock(st) {
			break
		}
	}
}

// seqCollectGotoLabels 递归收集语句列表里的标签（函数作用域）。
func seqCollectGotoLabels(list []ast.Stmt, out map[string]seqGotoTarget) {
	var walkList func([]ast.Stmt)
	var walkStmt func(ast.Stmt)
	walkList = func(stmts []ast.Stmt) {
		for i, st := range stmts {
			if lab, ok := st.(*ast.LabeledStmt); ok && lab.Label != nil {
				// 同名标签在合法 Go 里不会重复；重复时保留首个声明，
				// 不按扫描顺序猜。
				if _, exists := out[lab.Label.Name]; !exists {
					out[lab.Label.Name] = seqGotoTarget{list: stmts, idx: i}
				}
			}
			walkStmt(st)
		}
	}
	walkStmt = func(st ast.Stmt) {
		switch s := st.(type) {
		case nil:
			return
		case *ast.BlockStmt:
			walkList(s.List)
		case *ast.IfStmt:
			if s.Init != nil {
				walkStmt(s.Init)
			}
			walkList(s.Body.List)
			if s.Else != nil {
				walkStmt(s.Else)
			}
		case *ast.ForStmt:
			if s.Init != nil {
				walkStmt(s.Init)
			}
			if s.Post != nil {
				walkStmt(s.Post)
			}
			walkList(s.Body.List)
		case *ast.RangeStmt:
			walkList(s.Body.List)
		case *ast.SwitchStmt:
			walkStmt(s.Body)
		case *ast.TypeSwitchStmt:
			walkStmt(s.Body)
		case *ast.SelectStmt:
			walkStmt(s.Body)
		case *ast.CaseClause:
			walkList(s.Body)
		case *ast.CommClause:
			walkList(s.Body)
		case *ast.LabeledStmt:
			walkStmt(s.Stmt)
		}
	}
	walkList(list)
}
