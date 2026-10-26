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
	topic         string
	keyVar        string
	payloadVar    string
	assign        *ast.AssignStmt
	overwriteLine int
	readRoot      string
	readInitLine  int
	filters       []filterEvidence
	issue         string
}

// findRecv 解析 Receive 调用 + 返回键过滤 + 读取所在分支。
func findRecv(pf *parsedFunc, leaf string, readLine int) *recvEvidence {
	var recv *recvEvidence
	// 建立 IfStmt 父子关系（判断 readLine 落在哪个分支）
	type ifNode struct {
		node    *ast.IfStmt
		body    [2]int
		els     [2]int
		hasElse bool
	}
	var ifs []ifNode
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		ifs2, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		in := ifNode{node: ifs2,
			body: [2]int{pf.fset.Position(ifs2.Body.Lbrace).Line, pf.fset.Position(ifs2.Body.Rbrace).Line}}
		if ifs2.Else != nil {
			in.hasElse = true
			in.els = [2]int{pf.fset.Position(ifs2.Else.Pos()).Line, pf.fset.Position(ifs2.Else.End()).Line}
		}
		ifs = append(ifs, in)
		return true
	})

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
			e := &recvEvidence{line: pf.fset.Position(call.Lparen).Line, api: sel.Sel.Name,
				assign: as}
			if rv, ok := sel.X.(*ast.Ident); ok {
				e.apiType = varTypeMap(pf)[rv.Name]
			}
			for _, arg := range call.Args {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					e.topic = strings.Trim(lit.Value, "`\"")
					break
				}
			}
			// 位置对齐：`_, job := Receive(...)` 时 key 槽为空、payload=job
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
			recv = e
			return true
		}
		return true
	})
	if recv == nil {
		return nil
	}
	// 读取实例：必须是 Receive 返回载荷 job 或其别名（other.RecordID 不算）
	if recv.payloadVar == "" {
		recv.issue = "Receive 返回值未绑定载荷实例（无法核对读取实例）"
	} else if base, _, ok := fieldReadBase(pf, leaf, readLine); !ok {
		recv.issue = "读取行未找到载荷字段访问（无法核对实例）"
	} else {
		root, fromRecv, fresh := resolveInstanceRoot(pf, base, readLinePos(pf, readLine), 0)
		recv.readRoot = root
		if root != recv.payloadVar && !(fromRecv && root == recv.payloadVar) {
			recv.issue = "读取实例 " + shortExprText(root) + " 与接收载荷 " + recv.payloadVar +
				" 无值/别名关联（" + instanceOrigin(pf, root, readLine, fresh) + "）"
		}
	}

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
		usesKey := false
		keyFromReceive := false
		overwrite := 0
		switch e := expr.(type) {
		case *ast.Ident:
			if e.Name == recv.keyVar {
				if b := reachingBinding(pf, e.Name, be.Pos()); b != nil {
					if b.stmt == recv.assign {
						usesKey, keyFromReceive = true, true
					} else {
						overwrite = b.line
					}
				}
			}
		case *ast.SelectorExpr:
			if e.Sel.Name == leaf {
				// 载荷字段作为过滤键：字段实例必须是 Receive 载荷且未被覆盖
				if recv.payloadVar != "" {
					root, _, _ := resolveInstanceRoot(pf, e.X, be.Pos(), 0)
					if root == recv.payloadVar {
						if b := reachingBinding(pf, recv.payloadVar, be.Pos()); b != nil && b.stmt == recv.assign {
							usesKey, keyFromReceive = true, true
						}
					}
				}
			}
		}
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
		_ = keyFromReceive

		for _, in := range ifs {
			cond, ok := in.node.Cond.(*ast.BinaryExpr)
			if !ok || cond != be {
				continue
			}
			inThen := readLine >= in.body[0] && readLine <= in.body[1]
			inElse := in.hasElse && readLine >= in.els[0] && readLine <= in.els[1]
			if be.Op == token.EQL && inThen {
				f.readOK = true
			}
			if be.Op == token.NEQ && inElse {
				f.readOK = true
			}
			if f.readOK {
				f.readLine = readLine
			}
		}
		recv.filters = append(recv.filters, f)
		return true
	})
	return recv
}

// readLinePos 读取行对应的 token.Pos（取该行首个语句位置；用于到达定义判定）。
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

// shortExprText 实例名展示。
func shortExprText(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
