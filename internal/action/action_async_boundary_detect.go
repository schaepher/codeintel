package action

// todo15-6：边界证据的值/实例/路径关联（重写 todo14 的宽松识别）。
//
// 只从**实际写入实例与发送实参**取键值；只认 Receive 返回键参与的
// ==/!= 比较，并要求读取位于对应控制分支内。方法名/字符串相同、无关
// 调用里的常量、别的实例字段都不构成同记录证据。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

type parsedFunc struct {
	id     string
	pkg    string
	file   string
	src    []byte
	fset   *token.FileSet
	decl   *ast.FuncDecl
	parsed *ast.File
}

func (a *Actions) parseFuncNode(id string) *parsedFunc {
	n, err := a.repo.GetSymbol(domain.CanonicalID(id))
	if err != nil || n == nil || n.FilePath == "" {
		return nil
	}
	full := filepath.Join(a.repo.RepoPath(), filepath.FromSlash(n.FilePath))
	src, err := os.ReadFile(full)
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return nil
	}
	_, decl := findFuncDecl(fset, f, n.LineStart)
	if decl == nil || decl.Body == nil {
		return nil
	}
	return &parsedFunc{id: id, pkg: pkgOfID(domain.CanonicalID(id)), file: n.FilePath,
		src: src, fset: fset, decl: decl, parsed: f}
}

var (
	asyncProducerCalls = map[string]bool{"send": true, "sendmessage": true, "publish": true,
		"produce": true, "put": true, "save": true, "insert": true, "write": true, "store": true}
	asyncConsumerCalls = map[string]bool{"receive": true, "consumemessage": true, "consume": true,
		"subscribe": true, "read": true, "get": true, "find": true, "findbyid": true,
		"load": true, "fetch": true, "query": true}
)

type int2string struct {
	line  int
	value string
}

type keyLiteral struct {
	instance string
	value    string
	line     int
}

// fieldKeyLiterals 记录键字段的字面量（按实例名；字段赋值 + 复合字面量）。
func fieldKeyLiterals(pf *parsedFunc, leaf string) []keyLiteral {
	var out []keyLiteral
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != leaf {
					continue
				}
				inst, _ := sel.X.(*ast.Ident)
				if inst == nil || len(x.Rhs) != len(x.Lhs) {
					continue
				}
				if lit, ok := x.Rhs[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					out = append(out, keyLiteral{instance: inst.Name,
						value: strings.Trim(lit.Value, "`\""), line: pf.fset.Position(x.Pos()).Line})
				}
			}
		}
		return true
	})
	// 复合字面量：job := &Job{RecordID: "..."}（实例取赋值左侧名字）
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			inst, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			ast.Inspect(as.Rhs[i], func(m ast.Node) bool {
				cl, ok := m.(*ast.CompositeLit)
				if !ok {
					return true
				}
				for _, el := range cl.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					id, ok := kv.Key.(*ast.Ident)
					if !ok || id.Name != leaf {
						continue
					}
					if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						out = append(out, keyLiteral{instance: inst.Name,
							value: strings.Trim(lit.Value, "`\""), line: pf.fset.Position(cl.Pos()).Line})
					}
				}
				return true
			})
		}
		return true
	})
	return out
}

// varLiterals 局部变量直接赋的字面量（发送键变量用）。
func varLiterals(pf *parsedFunc) map[string][]keyLiteral {
	out := map[string][]keyLiteral{}
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			if lit, ok := as.Rhs[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out[id.Name] = append(out[id.Name], keyLiteral{instance: id.Name,
					value: strings.Trim(lit.Value, "`\""), line: pf.fset.Position(as.Pos()).Line})
			}
		}
		return true
	})
	return out
}

// sendEvidence 写入侧的实际发送证据。
type sendEvidence struct {
	line, apiLine int
	pos           token.Pos
	api           string
	apiType       string
	recvVar       string
	topic         string
	keyExpr       string
	keyValue      string
	keyLine       int
	keyInstance   string
	payloadInst   string
	topicLine     int
	issue         string
}

// findSend 从实际 Send 调用解析主题/键/实例；只看键实参（不拿别的字段替补）。
func findSend(pf *parsedFunc, leaf string) *sendEvidence {
	var sends []*sendEvidence
	asts := varLiterals(pf)
	keys := fieldKeyLiterals(pf, leaf)
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !asyncProducerCalls[strings.ToLower(sel.Sel.Name)] {
			return true
		}
		e := &sendEvidence{line: pf.fset.Position(call.Lparen).Line, pos: call.Pos(), api: sel.Sel.Name}
		e.recvVar = receiverVar(call.Fun)
		if rv, ok := sel.X.(*ast.Ident); ok {
			e.apiType = varTypeMap(pf)[rv.Name]
		}
		var lits []int2string
		var keyField *ast.SelectorExpr
		var keyVar string
		var payload string
		for _, arg := range call.Args {
			switch a := arg.(type) {
			case *ast.BasicLit:
				if a.Kind == token.STRING {
					lits = append(lits, int2string{line: pf.fset.Position(a.Pos()).Line,
						value: strings.Trim(a.Value, "`\"")})
				}
			case *ast.SelectorExpr:
				if a.Sel.Name == leaf {
					keyField = a
				}
				if id, ok := a.X.(*ast.Ident); ok && a.Sel.Name != leaf {
					payload = id.Name
				}
			case *ast.Ident:
				if keyField != nil || keyVar != "" || a.Name == "nil" {
					payload = a.Name
				} else if len(lits) > 0 {
					// 第二个非字面量实参：可能是键变量或载荷
					if _, has := asts[a.Name]; has {
						keyVar = a.Name
					} else {
						payload = a.Name
					}
				} else {
					keyVar = a.Name
				}
			case *ast.CompositeLit:
				payload = ""
			}
		}
		// 两个字符串字面量：第一个主题，第二个是**实际发送键**
		if len(lits) >= 2 {
			e.topic, e.topicLine = lits[0].value, lits[0].line
			e.keyValue, e.keyLine = lits[1].value, lits[1].line
		} else if len(lits) == 1 {
			e.topic, e.topicLine = lits[0].value, lits[0].line
		}
		if keyField != nil {
			inst, _ := keyField.X.(*ast.Ident)
			e.keyExpr = exprTextShallow(keyField)
			if inst != nil {
				e.keyInstance = inst.Name
				var vals []keyLiteral
				for _, k := range keys {
					if k.instance == inst.Name {
						vals = append(vals, k)
					}
				}
				if len(vals) == 1 {
					e.keyValue, e.keyLine = vals[0].value, vals[0].line
				} else if len(vals) > 1 {
					var vs []string
					for _, v := range vals {
						vs = append(vs, v.value)
					}
					sortStrings(vs)
					e.issue = "记录键实例有多个字面量（" + strings.Join(vs, ", ") +
						"；同字段不同实例/分支），无法证明实际值"
				} else if e.keyValue == "" {
					e.issue = "记录键不是可追踪字面量（" + e.keyExpr + "）"
				}
				if payload == "" {
					payload = inst.Name
				}
				if e.keyInstance != "" && payload != "" && e.keyInstance != payload {
					e.issue = "发送键字段与载荷实例不同（" + e.keyInstance + " vs " + payload + "）"
				}
			}
		} else if keyVar != "" && e.keyValue == "" {
			if vals := asts[keyVar]; len(vals) == 1 {
				e.keyValue, e.keyLine = vals[0].value, vals[0].line
				e.keyExpr = keyVar
			} else {
				e.issue = "发送键变量不是唯一字面量（" + keyVar + "）"
			}
		}
		if e.topic == "" {
			e.issue = "发送调用未提供主题/表字面量"
		}
		sends = append(sends, e)
		return true
	})
	if len(sends) == 0 {
		return nil
	}
	if len(sends) > 1 {
		// 同函数多次发送：键/主题不同 → 不能证明单次身份
		first := sends[0]
		for _, s := range sends[1:] {
			if s.topic != first.topic || s.keyValue != first.keyValue {
				return &sendEvidence{line: first.line, api: first.api, topic: first.topic,
					issue: "同函数存在多个发送调用且主题/键不同（同调用位置不同实参）"}
			}
		}
	}
	return sends[0]
}

// filterEvidence 读取侧过滤谓词（只认 Receive 返回键参与的 ==/!=）。

// findRecv 解析 Receive 调用 + 返回键过滤 + 读取所在分支。
