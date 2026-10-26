package action

import (
	"go/ast"
	"go/token"
	"strings"
)

// filterEvidence 读取侧过滤谓词（只认 Receive 返回键参与的 ==/!=）。
type filterEvidence struct {
	line     int
	value    string
	op       token.Token
	expr     string
	readOK   bool
	readLine int
}

type recvEvidence struct {
	line, apiLine int
	api           string
	apiType       string
	recvVar       string
	topic         string
	keyVar        string
	payloadVar    string
	assign        *ast.AssignStmt
	overwriteLine int
	readRoot      string
	readBindLine  int
	readFresh     bool
	filters       []filterEvidence
	issue         string
}

type recvIfNode struct {
	node    *ast.IfStmt
	body    [2]int
	els     [2]int
	hasElse bool
}

// findRecv 解析 Receive 调用 + 返回键过滤 + 读取所在分支。
// 多个 Receive 时选择**过滤谓词实际使用的那个**（不按出现顺序取最后一个）。
func findRecv(pf *parsedFunc, leaf string, readLine int) *recvEvidence {
	ifs := collectReceiverIfs(pf)
	recvs := collectReceiveCandidates(pf)
	if len(recvs) == 0 {
		return nil
	}
	var chosen *recvEvidence
	for _, r := range recvs {
		r.filters = collectKeyFilters(pf, r, leaf, readLine, ifs)
		if len(r.filters) > 0 {
			chosen = r
			break
		}
	}
	if chosen == nil {
		chosen = recvs[0]
	}
	validateReceiverRead(pf, chosen, leaf, readLine)
	return chosen
}

func collectReceiverIfs(pf *parsedFunc) []recvIfNode {
	var ifs []recvIfNode
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		x, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		in := recvIfNode{node: x,
			body: [2]int{pf.fset.Position(x.Body.Lbrace).Line, pf.fset.Position(x.Body.Rbrace).Line}}
		if x.Else != nil {
			in.hasElse = true
			in.els = [2]int{pf.fset.Position(x.Else.Pos()).Line, pf.fset.Position(x.Else.End()).Line}
		}
		ifs = append(ifs, in)
		return true
	})
	return ifs
}

// collectReceiveCandidates 所有 Receive 赋值（含 key/payload 绑定/接收者）。
func collectReceiveCandidates(pf *parsedFunc) []*recvEvidence {
	var out []*recvEvidence
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, rhs := range as.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !asyncConsumerCalls[strings.ToLower(sel.Sel.Name)] {
				continue
			}
			e := &recvEvidence{line: pf.fset.Position(call.Lparen).Line,
				api: sel.Sel.Name, assign: as, recvVar: receiverVar(call.Fun)}
			if rv, ok := sel.X.(*ast.Ident); ok {
				e.apiType = varTypeMap(pf)[rv.Name]
			}
			for _, arg := range call.Args {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					e.topic = strings.Trim(lit.Value, "`\"")
					break
				}
			}
			names := make([]string, len(as.Lhs))
			for i, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					names[i] = id.Name
				}
			}
			if len(names) > 0 {
				e.keyVar = names[0]
			}
			if len(names) > 1 {
				e.payloadVar = names[1]
			}
			if e.keyVar == "" {
				e.issue = "Receive 返回键未绑定（无法追踪过滤键）"
			}
			out = append(out, e)
			return true
		}
		return true
	})
	return out
}

// collectKeyFilters 该 Receive 的键/载荷字段参与的 ==/!= 与读取分支。
func collectKeyFilters(pf *parsedFunc, recv *recvEvidence, leaf string, readLine int, ifs []recvIfNode) []filterEvidence {
	var out []filterEvidence
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || (be.Op != token.EQL && be.Op != token.NEQ) {
			return true
		}
		lit, ok := be.Y.(*ast.BasicLit)
		expr := be.X
		if !ok || lit.Kind != token.STRING {
			lit, ok = be.X.(*ast.BasicLit)
			expr = be.Y
			if !ok || lit.Kind != token.STRING {
				return true
			}
		}
		usesKey, overwrite := filterKeyUse(pf, recv, leaf, expr, be.Pos())
		if overwrite > 0 && recv.issue == "" {
			recv.overwriteLine = overwrite
			recv.issue = "过滤键 " + recv.keyVar + " 在 L" + itoaSeq(overwrite) +
				" 被重赋值或遮蔽，比较 L" + itoaSeq(pf.fset.Position(be.Pos()).Line) +
				" 的值不再来自 Receive 返回键"
		}
		if !usesKey {
			return true
		}
		f := filterEvidence{line: pf.fset.Position(be.Pos()).Line,
			value: strings.Trim(lit.Value, "`\""), op: be.Op, expr: exprTextShallow(expr)}
		for _, in := range ifs {
			cond, ok := in.node.Cond.(*ast.BinaryExpr)
			if !ok || cond != be {
				continue
			}
			inThen := readLine >= in.body[0] && readLine <= in.body[1]
			inElse := in.hasElse && readLine >= in.els[0] && readLine <= in.els[1]
			if (be.Op == token.EQL && inThen) || (be.Op == token.NEQ && inElse) {
				f.readOK = true
				f.readLine = readLine
			}
		}
		out = append(out, f)
		return true
	})
	return out
}

// filterKeyUse 比较操作数是否依赖该 Receive 的键/载荷字段；返回覆盖行。
func filterKeyUse(pf *parsedFunc, recv *recvEvidence, leaf string, expr ast.Expr, pos token.Pos) (bool, int) {
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name != recv.keyVar {
			return false, 0
		}
		if b := reachingBinding(pf, e.Name, pos); b != nil {
			if b.stmt == recv.assign {
				return true, 0
			}
			return false, b.line
		}
	case *ast.SelectorExpr:
		if e.Sel.Name != leaf || recv.payloadVar == "" {
			return false, 0
		}
		root, _, _ := resolveInstanceRoot(pf, e.X, pos, 0)
		if root != recv.payloadVar {
			return false, 0
		}
		if b := reachingBinding(pf, recv.payloadVar, pos); b != nil && b.stmt == recv.assign {
			return true, 0
		}
	}
	return false, 0
}

// validateReceiverRead 读取实例必须来自选中 Receive 的载荷（或别名），且
// 未被同名覆盖/新建值替换（todo17-6）。
func validateReceiverRead(pf *parsedFunc, recv *recvEvidence, leaf string, readLine int) {
	if recv.payloadVar == "" {
		recv.issue = "Receive 返回值未绑定载荷实例（无法核对读取实例）"
		return
	}
	base, _, ok := fieldReadBase(pf, leaf, readLine)
	if !ok {
		recv.issue = "读取行未找到载荷字段访问（无法核对实例）"
		return
	}
	readPos := readLinePos(pf, readLine)
	root, fromRecv, fresh := resolveInstanceRoot(pf, base, readPos, 0)
	recv.readRoot, recv.readFresh = root, fresh
	if root == recv.payloadVar {
		if b := reachingBinding(pf, recv.payloadVar, readPos); b != nil && b.stmt != recv.assign {
			recv.readBindLine = b.line
		}
	}
	if root == recv.payloadVar && recv.readBindLine > 0 {
		recv.issue = "读取 " + recv.payloadVar + "." + leaf + "（L" + itoaSeq(readLine) +
			"）依赖 " + recv.payloadVar + " = 新值 的覆盖定义（L" + itoaSeq(recv.readBindLine) +
			"），已不依赖 Receive 返回载荷（L" + itoaSeq(pf.fset.Position(recv.assign.Pos()).Line) + "）"
		return
	}
	if root != recv.payloadVar || !fromRecv {
		recv.issue = "读取实例 " + shortExprText(root) + " 与接收载荷 " + recv.payloadVar +
			" 无值/别名关联（" + instanceOrigin(pf, root, readLine, fresh) + "）"
	}
}

// readLinePos 读取行对应的 token.Pos（用于到达定义判定）。
func readLinePos(pf *parsedFunc, line int) token.Pos {
	var pos token.Pos
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		if pf.fset.Position(n.Pos()).Line == line {
			if pos == token.NoPos || n.Pos() < pos {
				pos = n.Pos()
			}
			return false
		}
		return true
	})
	return pos
}

// instanceOrigin 读取实例的初始化依据（供诊断显示）。
func instanceOrigin(pf *parsedFunc, root string, readLine int, fresh bool) string {
	if root == "" {
		return "实例无法解析"
	}
	for _, b := range declaredBindings(pf, root) {
		if b.stmt == nil || b.rhs == nil || b.line == 0 {
			continue
		}
		if b.line <= readLine {
			if isReceiveCall(b.rhs) {
				return "来自 Receive 返回值"
			}
			if isFreshValue(b.rhs) {
				return "由构造/其他调用新建（L" + itoaSeq(b.line) + "）"
			}
			return "由 L" + itoaSeq(b.line) + " 赋值"
		}
	}
	if fresh {
		return "新建值"
	}
	return "未知来源"
}

func shortExprText(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
