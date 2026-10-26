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
	topic         string
	keyVar        string
	payloadVar    string
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
			e := &recvEvidence{line: pf.fset.Position(call.Lparen).Line, api: sel.Sel.Name}
			for _, arg := range call.Args {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					e.topic = strings.Trim(lit.Value, "`\"")
					break
				}
			}
			var names []string
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					names = append(names, id.Name)
				}
			}
			if len(names) > 0 {
				e.keyVar = names[0]
			}
			if len(names) > 1 {
				e.payloadVar = names[1]
			}
			if e.keyVar == "" {
				e.issue = "接收返回值未绑定（无法追踪过滤键）"
			}
			recv = e
			return true
		}
		return true
	})
	if recv == nil {
		return nil
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
		switch e := expr.(type) {
		case *ast.Ident:
			usesKey = e.Name == recv.keyVar
		case *ast.SelectorExpr:
			usesKey = e.Sel.Name == leaf
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
