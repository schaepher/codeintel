package action

// todo17-6：实际 API 目标 + 系统/配置绑定身份。
//
// 同接收者类型只是候选：必须解析实际被调方法（调用边目标，完整包/类型），
// 并核对发送/接收实例的初始化/配置绑定（同一声明或同非空配置字面量）。
// 未知绑定保守 unverified；已证明不同 → mismatch。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

type apiCallKey struct {
	funcID string
	line   int
}

// apiCallTargetIndex 调用点（源函数 + 行）→ 实际被调 canonical ID（一次加载）。
func (a *Actions) apiCallTargetIndex() map[apiCallKey]string {
	out := map[apiCallKey]string{}
	facts, err := a.repo.GetFactsByKinds(string(domain.FactCalls))
	if err != nil {
		return out
	}
	for _, f := range facts {
		line := metaLine(f.Metadata)
		if line <= 0 {
			continue
		}
		key := apiCallKey{funcID: string(f.SourceID), line: line}
		if _, ok := out[key]; !ok {
			out[key] = string(f.TargetID)
		}
	}
	return out
}

// apiTargetIdentity 目标方法 ID → (pkg, type, method)（仅方法形态）。
func apiTargetIdentity(id string) (pkg, typ, method string, ok bool) {
	i := strings.Index(id, ":(")
	if i < 0 {
		return "", "", "", false
	}
	j := strings.Index(id[i+2:], ").")
	if j < 0 {
		return "", "", "", false
	}
	pkg = strings.TrimPrefix(id[:i], "symbol:go:")
	rest := id[i+2:]
	return pkg, rest[:j], rest[j+2:], true
}

// receiverVar 选择器接收者变量名（bus.Send → bus）。
func receiverVar(fun ast.Expr) string {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

// declInit 变量在函数体或文件包级作用域的初始化表达式 + 声明位置。
func declInit(pf *parsedFunc, name string) (expr ast.Expr, line int, pkgLevel bool) {
	find := func(root ast.Node) (ast.Expr, int, bool) {
		var expr ast.Expr
		var line int
		ast.Inspect(root, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range x.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == name && len(x.Rhs) == len(x.Lhs) {
						if expr == nil {
							expr, line = x.Rhs[i], pf.fset.Position(x.Pos()).Line
						}
					}
				}
			case *ast.GenDecl:
				for _, sp := range x.Specs {
					vs, ok := sp.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, id := range vs.Names {
						if id.Name != name {
							continue
						}
						if expr == nil && len(vs.Values) == len(vs.Names) {
							expr, line = vs.Values[i], pf.fset.Position(x.Pos()).Line
						}
					}
				}
			}
			return true
		})
		return expr, line, expr != nil
	}
	// 函数内优先；同名包级变量由函数内遮蔽
	if e, l, ok := find(pf.decl.Body); ok {
		return e, l, false
	}
	if e, l, ok := find(pf.parsed); ok {
		return e, l, true
	}
	return nil, 0, false
}

// bindingInfo 实例绑定：同一声明 ID + 配置字面量签名 + 位置。
type bindingInfo struct {
	id          string
	sig         string
	line        int // 初始化/声明位置
	known       bool
	explicit    bool // 有字段配置（非空字面量）
	initialLine int  // 实例初始化行
	modLine     int  // 调用前最后一次配置覆盖行
	callLine    int  // 调用位置
	unknownWhy  string
}

func resolveBinding(pf *parsedFunc, varName string, depth int) bindingInfo {
	if varName == "" || depth > 4 {
		return bindingInfo{}
	}
	expr, line, pkgLevel := declInit(pf, varName)
	if expr == nil {
		return bindingInfo{id: "unknown:" + varName, line: line}
	}
	sig, explicit, ok := compositeConfig(expr)
	id := "local:" + pf.id + ":" + varName + ":" + itoaSeq(line)
	if pkgLevel {
		id = "pkg:" + pf.pkg + ":" + varName + ":" + itoaSeq(line)
	}
	// 别名：var a = b → 继续解析 b
	if e := unwrapAddr(expr); e != nil {
		if idExpr, isID := e.(*ast.Ident); isID {
			inner := resolveBinding(pf, idExpr.Name, depth+1)
			if inner.known || inner.id != "" {
				return inner
			}
		}
	}
	return bindingInfo{id: id, sig: sig, line: line, known: ok && (explicit || line > 0), explicit: explicit}
}

func unwrapAddr(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				e = x.X
				continue
			}
		case *ast.ParenExpr:
			e = x.X
			continue
		}
		return e
	}
}

// compositeConfig &T{Field: "v"} → 稳定签名；非字面量字段 → 不可证明。
func compositeConfig(e ast.Expr) (sig string, explicit, ok bool) {
	e = unwrapAddr(e)
	cl, isCl := e.(*ast.CompositeLit)
	if !isCl {
		return "", false, false
	}
	var parts []string
	known := true
	for _, el := range cl.Elts {
		kv, isKV := el.(*ast.KeyValueExpr)
		if !isKV {
			continue
		}
		key, isID := kv.Key.(*ast.Ident)
		if !isID {
			continue
		}
		val, isLit := kv.Value.(*ast.BasicLit)
		if !isLit {
			known = false
			continue
		}
		parts = append(parts, key.Name+"="+val.Value)
	}
	sortStrings(parts)
	if !known {
		return "", len(parts) > 0, false
	}
	if len(parts) == 0 {
		return "{}", false, true
	}
	return strings.Join(parts, ","), true, true
}

// sameBinding 比较发送/接收实例绑定；返回 (same, verdict, detail)。
func sameBinding(sender, receiver bindingInfo) (bool, string, string) {
	if sender.id == "" || receiver.id == "" {
		return false, "unverified", "系统/配置绑定未解析（实例声明缺失）"
	}
	if sender.id == receiver.id {
		return true, SeqVerdictCorroborated, "同一实例声明（" + sender.id + "）"
	}
	// 不同实例：只有非空配置字面量相同才可证明同一系统配置
	if sender.explicit && receiver.explicit && sender.known && receiver.known && sender.sig == receiver.sig {
		return true, SeqVerdictCorroborated, "同类型 + 同配置字面量（" + sender.sig + "）"
	}
	if sender.known && receiver.known && sender.sig != "" && receiver.sig != "" && sender.sig != receiver.sig {
		return false, "mismatch", "系统/配置绑定不同：" + bindingText(sender) + " vs " + bindingText(receiver)
	}
	return false, "unverified", "系统/配置绑定未建模（" + bindingText(sender) + " vs " + bindingText(receiver) + "）"
}

// resolveBindingAt 调用位置（token.Pos）的绑定：最新实例定义 + 调用前
// 配置覆盖。初始化快照不能直接作为调用时身份依据（todo18-6）。

// packageDeclLine 包级变量声明行（局部重赋值不覆盖首次声明位置）。

// latestInstance 调用前最新的实例定义（局部重赋值优先；否则包级初始化）。

// applyFieldModifications 调用前的接收者字段覆盖 / 别名修改 / 未知调用。
