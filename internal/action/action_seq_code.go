package action

// R95 迁移：`query sequence --code` 查询逻辑（原 cli/query_sequence_code.go
// + query_sequence_entry.go）——代码级时序图（源码 AST 转时序）。
// 纯语法解析不依赖类型信息；行号对齐索引调用边定位被调符号（嵌套
// 展开）。cli 只做参数解析与输出渲染（mermaid/文本树留 cli）。

import (
	"go/ast"
	"go/token"
	"strings"

	"go.uber.org/zap"
)

// CodeSequenceRequest query sequence --code 参数。
type CodeSequenceRequest struct {
	Target       string   // 符号名或 canonical ID
	RepoAbs      string   // 仓库绝对路径（源码读取）
	Depth        int      // 嵌套层级（1 = 只本函数；>1 递归展开被调函数内部）
	StopPackages []string // 停止包列表（cli 从 config.yaml 读取——命中不深入）
	// 内部：递归展开路径防环（R97——grpc 实现内部调接口、接口具体化
	// 回到同一 grpc 实现时自环；路径语义：进入标记、退出清除，不同
	// 分支互不影响）
	visited map[string]bool
}

// CodeSeqNode 代码级时序节点（树——分支/循环/嵌套调用）。
type CodeSeqNode struct {
	Kind     string         // call | branch | loop
	Label    string         // call: 调用名（obj.Method/fn）；branch: 条件；loop: 循环条件
	Actor    string         // R83：call 参与者（调用对象——s.manager/t.repo/ic；函数为函数名）
	Type     string         // R83：参与者短类型名（包最后路径段.类型名——索引实现类型）
	DeclType string         // P0-5：receiver 字段声明类型（接口——"声明接口"）
	ImplType string         // P0-5：数据流实现类型（R97-2 具体化命中时——声明接口 → 数据流实现双行）
	Args     []string       // R83：参数类型短名（消息线内容）
	Returns  []string       // R83：返回类型短名（return 线内容）
	Line     int            // 源码行号
	Nodes    []*CodeSeqNode // 子节点（branch: then 分支；loop: 循环体；call: 嵌套展开的被调函数调用）
	Else     []*CodeSeqNode // branch: else 分支（可选）
}

// CodeSequence 解析目标函数的代码级时序（读源码 AST——纯语法不依赖
// 类型信息）。depth 为嵌套层级（1 = 只本函数；>1 递归展开被调函数
// 内部——行号对齐索引调用边定位被调符号）。符号解析失败返回 nil。
// R84：接口方法入口（grpc 服务入口接口——动态入口无方法体，如
// (OrderServiceServer).SubmitOrder）→ 具体化到实现方法再解析。
func (a *Actions) CodeSequence(req CodeSequenceRequest) (*CodeSeqNode, error) {
	logger := zap.L()
	logger.Info("enter (Actions).CodeSequence", zap.String("target", req.Target), zap.Int("depth", req.Depth))
	defer logger.Info("exit (Actions).CodeSequence")
	n, err := a.ResolveSymbol(req.Target)
	if err != nil {
		return nil, err
	}
	if req.visited == nil {
		req.visited = map[string]bool{}
	}
	root := codeSeqForSymbol(a, req, string(n.ID), req.Depth)
	if root == nil && strings.Contains(string(n.ID), ":(") {
		if impl, ok := a.InterfaceMethodImpl(string(n.ID)); ok {
			root = codeSeqForSymbol(a, req, impl, req.Depth)
		}
	}
	return root, nil
}

// codeSeqForSymbol 按符号 ID 解析函数体（递归入口——嵌套展开用
// canonical ID 直接解析；depth = 当前剩余嵌套层级）。

// recvTypeName receiver 类型 AST → 类型名（*T / T → T；跨包形态
// pkg.T → T——同包字段写入常见）。

// walkStmts 遍历语句列表生成步骤（调用语句 / 分支 / 循环 / 块递归）。
// f = 当前函数所在文件（局部变量 DI 注入数据流查询用）；curPkg = 当前
// 函数包路径（P0-6）。tgts = 调用点 → 被调符号映射（R99-3：offset
// 优先——同行多调用区分）。
func walkStmts(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte, stmts []ast.Stmt, tgts *seqTargets, depth int, recvType string, recvDecl map[string]string, f *ast.File, curPkg string) []*CodeSeqNode {
	var out []*CodeSeqNode
	for _, st := range stmts {
		out = append(out, walkStmt(a, req, fset, src, st, tgts, depth, recvType, recvDecl, f, curPkg)...)
	}
	return out
}

// walkStmt 单条语句 → 步骤（可能多条：赋值内多个调用）。
func walkStmt(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte, stmt ast.Stmt, tgts *seqTargets, depth int, recvType string, recvDecl map[string]string, f *ast.File, curPkg string) []*CodeSeqNode {
	callNode := func(fun ast.Expr, pos token.Position) *CodeSeqNode {
		node := &CodeSeqNode{Kind: "call", Label: callLabel(fset, src, fun), Actor: callActor(fset, src, fun), Line: pos.Line}
		// R83：签名提取（类型节点用 AST 方法名构造 (Impl).Method 再查）
		if tid, ok := tgts.lookup(pos); ok {
			sigID := tid
			if !strings.Contains(tid, ":(") {
				if sel, isSel := fun.(*ast.SelectorExpr); isSel {
					sigID = GrpcMethodEntryID(tid, sel.Sel.Name)
				}
			}
			node.Type = implTypeShort(tid)
			if args, rets, ok2 := sigTypesOf(a, sigID); ok2 {
				node.Args, node.Returns = args, rets
			}
		}
		// R81：嵌套展开（类型节点用 AST 方法名构造 (Impl).Method；函数失败回退原 ID）
		if depth > 1 {
			if tid, ok := tgts.lookup(pos); ok {
				// R83：停止包配置——命中不深入（节点保留，Nodes 空）
				if seqStopPkgHit(tid, req.StopPackages) {
					return node
				}
				// R97-2：receiver 字段数据流优先——s.manager.SubmitOrder
				// 的 s.manager 赋值来源（构造函数 &orderManagerImpl{}）
				// → 直接落到具体实现（比接口类型匹配更精确）
				if sel, isSel := fun.(*ast.SelectorExpr); isSel && recvType != "" {
					if field, ok2 := receiverFieldSel(sel, recvType); ok2 {
						if implMethod := a.receiverFieldImpl(req, recvType, field, sel.Sel.Name); implMethod != "" {
							// P0-5：数据流实现类型 + 字段声明类型（参与
							// 者第二行——声明接口 → 数据流实现双行显示）
							node.ImplType = implTypeShort(implMethod)
							if decl, ok := recvDecl[field]; ok {
								node.DeclType = decl
							}
							if child := codeSeqForSymbol(a, req, implMethod, depth-1); child != nil {
								node.Nodes = child.Nodes
								return node
							}
						}
					}
				}
				// P0-6：局部变量 DI 注入——m.SubmitOrder 的 m 初始化自
				// 构造器（m := newX()，newX 返回接口、函数体 return 具体
				// 实现）或字面量 → 具体化到实现方法
				if sel, isSel := fun.(*ast.SelectorExpr); isSel {
					if id, ok := sel.X.(*ast.Ident); ok {
						if implMethod := a.localVarImpl(req, f, curPkg, id.Name, sel.Sel.Name); implMethod != "" {
							node.ImplType = implTypeShort(implMethod)
							if child := codeSeqForSymbol(a, req, implMethod, depth-1); child != nil {
								node.Nodes = child.Nodes
								return node
							}
						}
					}
				}
				// P0-7：接口方法 fallback——数据流/localVar 找不到（参数
				// 注入形态：接口实现由外部 DI 框架注入构造器参数，函数
				// 内无显式创建）→ 接口实现枚举（InterfaceMethodImpl——
				// 业务实现优先、grpc 实现排后防自环）
				if strings.Contains(tid, ":(") {
					if impl, ok := a.InterfaceMethodImpl(tid); ok {
						node.ImplType = implTypeShort(impl)
						if child := codeSeqForSymbol(a, req, impl, depth-1); child != nil {
							node.Nodes = child.Nodes
							return node
						}
					}
				}
				if !strings.Contains(tid, ":(") {
					if sel, isSel := fun.(*ast.SelectorExpr); isSel {
						if child := codeSeqForSymbol(a, req, GrpcMethodEntryID(tid, sel.Sel.Name), depth-1); child != nil {
							node.Nodes = child.Nodes
							return node
						}
					}
				}
				if child := codeSeqForSymbol(a, req, tid, depth-1); child != nil {
					node.Nodes = child.Nodes
				}
			}
		}
		return node
	}
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			return []*CodeSeqNode{callNode(call.Fun, fset.Position(call.Pos()))}
		}
	case *ast.AssignStmt:
		var out []*CodeSeqNode
		for _, rhs := range s.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok {
				out = append(out, callNode(call.Fun, fset.Position(call.Pos())))
			}
		}
		return out
	case *ast.IfStmt:
		node := &CodeSeqNode{Kind: "branch", Label: seqExprText(fset, src, s.Cond), Line: fset.Position(s.Cond.Pos()).Line}
		node.Nodes = walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg)
		if blk, ok := s.Else.(*ast.BlockStmt); ok {
			node.Else = walkStmts(a, req, fset, src, blk.List, tgts, depth, recvType, recvDecl, f, curPkg)
		} else if elseIf, ok := s.Else.(*ast.IfStmt); ok {
			node.Else = walkStmt(a, req, fset, src, elseIf, tgts, depth, recvType, recvDecl, f, curPkg) // else if 链（渲染为 else 内分支）
		}
		return []*CodeSeqNode{node}
	case *ast.ForStmt:
		label := seqExprText(fset, src, s.Cond)
		if label == "" {
			label = "无限循环"
		}
		return []*CodeSeqNode{{Kind: "loop", Label: label, Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg)}}
	case *ast.RangeStmt:
		return []*CodeSeqNode{{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X), Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg)}}
	case *ast.BlockStmt:
		return walkStmts(a, req, fset, src, s.List, tgts, depth, recvType, recvDecl, f, curPkg)
	case *ast.DeferStmt:
		return []*CodeSeqNode{callNode(s.Call.Fun, fset.Position(s.Call.Pos()))}
	case *ast.GoStmt:
		return []*CodeSeqNode{callNode(s.Call.Fun, fset.Position(s.Call.Pos()))}
	case *ast.ReturnStmt:
		var out []*CodeSeqNode
		for _, r := range s.Results {
			if call, ok := r.(*ast.CallExpr); ok {
				out = append(out, callNode(call.Fun, fset.Position(call.Pos())))
			}
		}
		return out
	case *ast.SwitchStmt:
		// R82：switch 分派（manager.SubmitOrder 实测——data.Type 分支里
		// 才是真实业务调用；每 case 一个子分支）
		tag := seqExprText(fset, src, s.Tag)
		if tag == "" {
			tag = "switch"
		}
		node := &CodeSeqNode{Kind: "branch", Label: tag, Line: fset.Position(s.Pos()).Line}
		for _, st := range s.Body.List {
			if cc, ok := st.(*ast.CaseClause); ok {
				caseNode := &CodeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc), Line: fset.Position(cc.Pos()).Line}
				caseNode.Nodes = walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*CodeSeqNode{node}
	case *ast.TypeSwitchStmt:
		node := &CodeSeqNode{Kind: "branch", Label: "type switch", Line: fset.Position(s.Pos()).Line}
		for _, st := range s.Body.List {
			if cc, ok := st.(*ast.CaseClause); ok {
				caseNode := &CodeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc), Line: fset.Position(cc.Pos()).Line}
				caseNode.Nodes = walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*CodeSeqNode{node}
	}
	return nil
}

// receiverFieldSel 调用 fun（s.manager.SubmitOrder）是否为 receiver
// 字段方法调用——s 是当前方法 receiver 名（recvType 对应）、X 是
// SelectorExpr（s.manager）→ 返回字段名 manager。

// receiverFieldImpl 数据流具体化（R97-2）：receiver 字段（s.manager）
// 的赋值来源类型——查摘要 direct_write 写入函数 → 读源码 AST 找
// 赋值表达式（x.manager = &orderManagerImpl{}）的具体类型 → 构造
// (Impl).Method。找不到（无写入摘要/赋值形态不支持）返回空——
// 调用方 fallback 接口类型匹配（InterfaceMethodImpl）。

// fieldWriteType 读写入函数源码，找 "x.<field> = <X>" 赋值中 X 的
// 类型名（&orderManagerImpl{} → orderManagerImpl；构造器调用
// newOrderManager() → newOrderManager 的返回？MVP：字面量与同包
// Ident）。返回 (类型名, 包路径)。

// exprTypeName 类型表达式 → 短名（Ident → 名；SelectorExpr pkg.T → T）
// ——P0-5 receiver 字段声明类型提取。
func exprTypeName(t ast.Expr) string {
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	if sel, ok := t.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}
