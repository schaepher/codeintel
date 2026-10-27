package action

import "go/ast"
import "go/token"
import "strings"

// litVal 抽象值：已知字符串字面量 / 未知（含声明存在但值不可判定）。
type litVal struct {
	val    string
	known  bool
	line   int    // 定义位置（赋值/声明行——证据）
	src    string // todo13-3：链式来源（literal@Lx → arg@Ly）
	reason string // todo13-3：未知原因（call-may-write / analysis-depth-limit）
	alias  string // todo13-3：取址别名（p := &x 时 p.alias=x）
}

// litScopes 词法作用域栈（形参/局部变量 → 抽象值）。
type litScopes struct {
	scopes  []map[string]litVal
	limited bool // todo13-3：分析深度/扫描上限触发（不得静默保留确定值）
}

func newLitScopes(params *ast.FieldList, incoming, incomingSrc []string) *litScopes {
	s := &litScopes{scopes: []map[string]litVal{{}}}
	if params == nil {
		return s
	}
	i := 0
	for _, f := range params.List {
		nn := len(f.Names)
		if nn == 0 {
			nn = 1
		}
		for j := 0; j < nn; j++ {
			name := ""
			if j < len(f.Names) {
				name = f.Names[j].Name
			}
			if name != "" {
				v := litVal{}
				if i < len(incoming) && incoming[i] != "" {
					v = litVal{val: incoming[i], known: true}
					if i < len(incomingSrc) {
						v.src = incomingSrc[i]
					}
				}
				s.scopes[0][name] = v
			}
			i++
		}
	}
	return s
}
func (s *litScopes) push() { s.scopes = append(s.scopes, map[string]litVal{}) }
func (s *litScopes) pop()  { s.scopes = s.scopes[:len(s.scopes)-1] }
func (s *litScopes) lookup(name string) (litVal, bool) {
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if v, ok := s.scopes[i][name]; ok {
			return v, true
		}
	}
	return litVal{}, false
}

// assign 更新最近的既有声明；没有声明则落在最内层作用域。
func (s *litScopes) assign(name string, v litVal) {
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if _, ok := s.scopes[i][name]; ok {
			s.scopes[i][name] = v
			return
		}
	}
	s.scopes[len(s.scopes)-1][name] = v
}

// declare 在当前作用域声明（:= / var 的遮蔽语义）。
func (s *litScopes) declare(name string, v litVal) {
	s.scopes[len(s.scopes)-1][name] = v
}
func (s *litScopes) clone() *litScopes {
	out := &litScopes{scopes: make([]map[string]litVal, len(s.scopes))}
	for i, sc := range s.scopes {
		m := make(map[string]litVal, len(sc))
		for k, v := range sc {
			m[k] = v
		}
		out.scopes[i] = m
	}
	return out
}

// assignMerged 多分支合流：所有分支同值才保留，否则未知（保守，不猜）。
func assignMerged(dst *litScopes, branches ...*litScopes) {
	if len(branches) == 0 || len(branches[0].scopes) == 0 {
		return
	}
	dst.scopes = make([]map[string]litVal, len(branches[0].scopes))
	for i := range branches[0].scopes {
		m := map[string]litVal{}
		for k, v := range branches[0].scopes[i] {
			m[k] = v
		}
		dst.scopes[i] = m
	}
	for _, b := range branches[1:] {
		for i := range dst.scopes {
			if i >= len(b.scopes) {
				continue
			}
			for k, cur := range dst.scopes[i] {
				v, ok := b.scopes[i][k]
				if !ok || cur.known != v.known || cur.val != v.val {
					dst.scopes[i][k] = litVal{}
				}
			}
		}
	}
}

// evalLitExpr 表达式 → 抽象值（字符串字面量 / 已知变量 / 取址别名；其余未知）。
func evalLitExpr(fset *token.FileSet, e ast.Expr, s *litScopes) litVal {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			line := fset.Position(x.Pos()).Line
			return litVal{val: strings.Trim(x.Value, "\"`"), known: true,
				line: line, src: "literal@L" + itoaSeq(line)}
		}
	case *ast.Ident:
		if v, ok := s.lookup(x.Name); ok {
			return v
		}
	case *ast.ParenExpr:
		return evalLitExpr(fset, x.X, s)
	case *ast.UnaryExpr:

		if x.Op == token.AND {
			if id, ok := x.X.(*ast.Ident); ok {
				return litVal{alias: id.Name}
			}
		}
	}
	return litVal{}
}

// applyCallInvalidation 调用可能通过指针改写变量：实参里的 &x 或取址别名。
// todo13-3：无法证明回调未改写时，旧字面量定义必须失效（不得静默保留）。
func applyCallInvalidation(fset *token.FileSet, src []byte, expr ast.Expr, s *litScopes) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return
	}
	line := fset.Position(call.Pos()).Line
	callee := callLabel(fset, src, call.Fun)
	for _, arg := range call.Args {
		switch a := arg.(type) {
		case *ast.UnaryExpr:
			if a.Op == token.AND {
				if id, ok := a.X.(*ast.Ident); ok {
					s.invalidate(id.Name, "call-may-write:"+callee+"@L"+itoaSeq(line))
				}
			}
		case *ast.Ident:
			if v, ok := s.lookup(a.Name); ok && v.alias != "" {
				s.invalidate(v.alias, "call-may-write:"+callee+"@L"+itoaSeq(line))
			}
		case *ast.CallExpr:
			applyCallInvalidation(fset, src, a, s)
		}
	}
}

// invalidate 将变量置为未知（保留原因与位置——证据用）。
func (s *litScopes) invalidate(name, reason string) {
	v, ok := s.lookup(name)
	if !ok {
		return
	}
	v.known = false
	v.val = ""
	v.alias = ""
	v.reason = reason
	if i := strings.LastIndex(reason, "@L"); i >= 0 {
		v.src = "call@L" + reason[i+2:]
	} else {
		v.src = "call"
	}
	s.assign(name, v)
}

// paramIndex 形参名在签名中的位置（跳过 receiver）。
func paramIndex(params *ast.FieldList, name string) int {
	if params == nil {
		return -1
	}
	i := 0
	for _, f := range params.List {
		nn := len(f.Names)
		if nn == 0 {
			nn = 1
		}
		for j := 0; j < nn; j++ {
			if j < len(f.Names) && f.Names[j].Name == name {
				return i
			}
			i++
		}
	}
	return -1
}

// lastSymName canonical ID 末段（方法形态 (T).M 取 M 之前的调用名匹配用）。
func lastSymName(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.LastIndex(id, ")."); i >= 0 {
		return id[i+2:]
	}
	return id
}
func (s *implState) clone() *implState {
	out := &implState{defs: make([]implDef, len(s.defs))}
	copy(out.defs, s.defs)
	return out
}
