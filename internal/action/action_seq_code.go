package action

// R95 迁移：`query sequence --code` 查询逻辑（原 cli/query_sequence_code.go
// + query_sequence_entry.go）——代码级时序图（源码 AST 转时序）。
// 纯语法解析不依赖类型信息；行号对齐索引调用边定位被调符号（嵌套
// 展开）。cli 只做参数解析与输出渲染（mermaid/文本树留 cli）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
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
	Kind    string         // call | branch | loop
	Label   string         // call: 调用名（obj.Method/fn）；branch: 条件；loop: 循环条件
	Actor   string         // R83：call 参与者（调用对象——s.manager/t.repo/ic；函数为函数名）
	Type    string         // R83：参与者短类型名（包最后路径段.类型名——索引实现类型）
	Args    []string       // R83：参数类型短名（消息线内容）
	Returns []string       // R83：返回类型短名（return 线内容）
	Line    int            // 源码行号
	Nodes   []*CodeSeqNode // 子节点（branch: then 分支；loop: 循环体；call: 嵌套展开的被调函数调用）
	Else    []*CodeSeqNode // branch: else 分支（可选）
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
func codeSeqForSymbol(a *Actions, req CodeSequenceRequest, symID string, depth int) *CodeSeqNode {
	// R97：路径防环——展开链上已出现的符号再次展开 → 不再深入
	// （grpc 实现内部调接口、接口具体化回到同一 grpc 实现的自环）
	if req.visited[symID] {
		return nil
	}
	req.visited[symID] = true
	defer delete(req.visited, symID)
	n, err := a.repo.GetSymbol(domain.CanonicalID(symID))
	if err != nil || n.FilePath == "" {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(req.RepoAbs, n.FilePath))
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return nil
	}
	// 定位目标函数（LineStart 在函数体范围内）
	var fn *ast.FuncDecl
	ast.Inspect(f, func(node ast.Node) bool {
		if fd, ok := node.(*ast.FuncDecl); ok {
			start := fset.Position(fd.Pos()).Line
			end := fset.Position(fd.End()).Line
			if n.LineStart >= start && n.LineStart <= end {
				fn = fd
				return false
			}
		}
		return true
	})
	if fn == nil {
		return nil
	}

	lineTargets := map[int]string{}
	if facts, err := a.CalleesConcrete(domain.CanonicalID(symID), 1); err == nil {
		for _, f := range facts {
			if ln, ok := f.Metadata["line_num"].(float64); ok {
				lineTargets[int(ln)] = string(f.TargetID)
			}
		}
	}
	root := &CodeSeqNode{Kind: "call", Label: fn.Name.Name, Line: n.LineStart}
	root.Nodes = walkStmts(a, req, fset, src, fn.Body.List, lineTargets, depth)
	return root
}

// walkStmts 遍历语句列表生成步骤（调用语句 / 分支 / 循环 / 块递归）。
func walkStmts(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte, stmts []ast.Stmt, lineTargets map[int]string, depth int) []*CodeSeqNode {
	var out []*CodeSeqNode
	for _, st := range stmts {
		out = append(out, walkStmt(a, req, fset, src, st, lineTargets, depth)...)
	}
	return out
}

// walkStmt 单条语句 → 步骤（可能多条：赋值内多个调用）。
func walkStmt(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte, stmt ast.Stmt, lineTargets map[int]string, depth int) []*CodeSeqNode {
	callNode := func(fun ast.Expr, line int) *CodeSeqNode {
		node := &CodeSeqNode{Kind: "call", Label: callLabel(fset, src, fun), Actor: callActor(fset, src, fun), Line: line}
		// R83：签名提取（类型节点用 AST 方法名构造 (Impl).Method 再查）
		if tid, ok := lineTargets[line]; ok {
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
			if tid, ok := lineTargets[line]; ok {
				// R83：停止包配置——命中不深入（节点保留，Nodes 空）
				if seqStopPkgHit(tid, req.StopPackages) {
					return node
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
			return []*CodeSeqNode{callNode(call.Fun, fset.Position(call.Pos()).Line)}
		}
	case *ast.AssignStmt:
		var out []*CodeSeqNode
		for _, rhs := range s.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok {
				out = append(out, callNode(call.Fun, fset.Position(call.Pos()).Line))
			}
		}
		return out
	case *ast.IfStmt:
		node := &CodeSeqNode{Kind: "branch", Label: seqExprText(fset, src, s.Cond), Line: fset.Position(s.Cond.Pos()).Line}
		node.Nodes = walkStmts(a, req, fset, src, s.Body.List, lineTargets, depth)
		if blk, ok := s.Else.(*ast.BlockStmt); ok {
			node.Else = walkStmts(a, req, fset, src, blk.List, lineTargets, depth)
		} else if elseIf, ok := s.Else.(*ast.IfStmt); ok {
			node.Else = walkStmt(a, req, fset, src, elseIf, lineTargets, depth) // else if 链（渲染为 else 内分支）
		}
		return []*CodeSeqNode{node}
	case *ast.ForStmt:
		label := seqExprText(fset, src, s.Cond)
		if label == "" {
			label = "无限循环"
		}
		return []*CodeSeqNode{{Kind: "loop", Label: label, Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(a, req, fset, src, s.Body.List, lineTargets, depth)}}
	case *ast.RangeStmt:
		return []*CodeSeqNode{{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X), Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(a, req, fset, src, s.Body.List, lineTargets, depth)}}
	case *ast.BlockStmt:
		return walkStmts(a, req, fset, src, s.List, lineTargets, depth)
	case *ast.DeferStmt:
		return []*CodeSeqNode{callNode(s.Call.Fun, fset.Position(s.Call.Pos()).Line)}
	case *ast.GoStmt:
		return []*CodeSeqNode{callNode(s.Call.Fun, fset.Position(s.Call.Pos()).Line)}
	case *ast.ReturnStmt:
		var out []*CodeSeqNode
		for _, r := range s.Results {
			if call, ok := r.(*ast.CallExpr); ok {
				out = append(out, callNode(call.Fun, fset.Position(call.Pos()).Line))
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
				caseNode.Nodes = walkStmts(a, req, fset, src, cc.Body, lineTargets, depth)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*CodeSeqNode{node}
	case *ast.TypeSwitchStmt:
		node := &CodeSeqNode{Kind: "branch", Label: "type switch", Line: fset.Position(s.Pos()).Line}
		for _, st := range s.Body.List {
			if cc, ok := st.(*ast.CaseClause); ok {
				caseNode := &CodeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc), Line: fset.Position(cc.Pos()).Line}
				caseNode.Nodes = walkStmts(a, req, fset, src, cc.Body, lineTargets, depth)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*CodeSeqNode{node}
	}
	return nil
}
