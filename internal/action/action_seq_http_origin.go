package action

// todo25 §4.3：HTTP origin 证明（注册函数内的直接语句）。
//
// 首版限定：同一函数直接语句中的 `http.Handle/HandleFunc(path, handler)` 与
// 后续 `http.ListenAndServe(<字面量 addr>, nil)`；addr 必须与 URL host:port
// 完全相等。读不到/解析失败/注册点不是直接语句都返回 readOK=false 或 ok=false
// ——绝不把"没读到"当"没有注册"。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqHTTPProveRoute 证明一条 http_route 注册的 origin。
// readOK=false 表示源码读取/解析/符号定位失败（候选不完整）。
func seqHTTPProveRoute(repoPath string, acts *Actions, rt *domain.CodeEntity,
	wantHost string, cache map[string]*seqParsedFile) (t seqHTTPTarget, ok bool, readOK bool) {
	rel, line, perr := splitRegisterSite(rt.Property("register"))
	if perr != nil {
		return seqHTTPTarget{}, false, true
	}
	fset, f, err := seqHTTPParseFile(repoPath, rel, cache)
	if err != nil {
		return seqHTTPTarget{}, false, false
	}
	fn := seqFuncAtLine(fset, f, line)
	if fn == nil {
		return seqHTTPTarget{}, false, false
	}
	routePath, addr, hasCall := seqHTTPDirectOrigin(fset, fn, line)
	if !hasCall {
		return seqHTTPTarget{}, false, true
	}
	if routePath != "" && routePath != rt.Property("path") {
		return seqHTTPTarget{}, false, true
	}
	if addr == "" || wantHost == "" || addr != wantHost {
		return seqHTTPTarget{}, false, true
	}
	handler := rt.Property("handler_id")
	sym, err := acts.repo.GetSymbol(domain.CanonicalID(handler))
	if err != nil {
		return seqHTTPTarget{}, false, false
	}
	return seqHTTPTarget{repo: repoPath, acts: acts, route: rt, handler: handler,
		file: sym.FilePath, line: sym.LineStart, addr: addr}, true, true
}

// splitRegisterSite "relative.go:123" → (relative.go, 123)。
func splitRegisterSite(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i <= 0 {
		return "", 0, errHTTP("注册点缺少 file:line")
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n <= 0 {
		return "", 0, errHTTP("注册点行号非法")
	}
	return filepath.FromSlash(s[:i]), n, nil
}

// seqHTTPParseFile 解析仓库内文件（请求内缓存；失败也缓存 readErr）。
func seqHTTPParseFile(repoPath, rel string, cache map[string]*seqParsedFile) (
	*token.FileSet, *ast.File, error) {
	key := repoPath + "\x00" + rel
	if cache != nil {
		if pf, hit := cache[key]; hit {
			if pf.err != nil {
				return nil, nil, pf.err
			}
			return pf.fset, pf.file, nil
		}
	}
	src, err := os.ReadFile(filepath.Join(repoPath, rel))
	if err != nil {
		if cache != nil {
			cache[key] = &seqParsedFile{err: err}
		}
		return nil, nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		if cache != nil {
			cache[key] = &seqParsedFile{err: err}
		}
		return nil, nil, err
	}
	if cache != nil {
		cache[key] = &seqParsedFile{fset: fset, file: f, src: src}
	}
	return fset, f, nil
}

// seqFuncAtLine 源码行号所在的函数声明（注册点必须在函数体内）。
func seqFuncAtLine(fset *token.FileSet, f *ast.File, line int) *ast.FuncDecl {
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		start := fset.Position(fd.Pos()).Line
		end := fset.Position(fd.End()).Line
		if line >= start && line <= end {
			return fd
		}
	}
	return nil
}

// seqHTTPDirectOrigin 在注册函数的**直接语句**里找 Handle/HandleFunc 与
// ListenAndServe(<字面量 addr>, nil)。返回注册 path 字面量与监听地址。
func seqHTTPDirectOrigin(fset *token.FileSet, fn *ast.FuncDecl, line int) (
	routePath, addr string, ok bool) {
	hasHandle := false
	for _, st := range fn.Body.List {
		es, isExpr := st.(*ast.ExprStmt)
		if !isExpr {
			continue
		}
		call, isCall := es.X.(*ast.CallExpr)
		if !isCall {
			continue
		}
		name := seqHTTPCalleeName(call.Fun)
		switch name {
		case "HandleFunc", "Handle":
			if fset.Position(call.Pos()).Line != line {
				continue
			}
			if len(call.Args) > 0 {
				routePath = seqStringLiteral(call.Args[0])
			}
			hasHandle = true
		case "ListenAndServe":
			if len(call.Args) < 2 || !seqIsNil(call.Args[1]) {
				continue
			}
			addr = seqStringLiteral(call.Args[0])
		}
	}
	if !hasHandle || addr == "" {
		return "", "", false
	}
	return routePath, addr, true
}

// seqHTTPCalleeName 调用的方法/函数短名（http.HandleFunc / mux.HandleFunc）。
func seqHTTPCalleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	}
	return ""
}

func seqStringLiteral(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

func seqIsNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// seqHTTPExpandRemote 跨仓展开 handler 子树（源码/包集合/文件缓存以目标仓库
// 为准；视图由外层请求统一关闭）。
func seqHTTPExpandRemote(target *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	entry, remoteRepo string, depth int) bool {
	if target == nil {
		return false
	}
	childReq := req
	childReq.Target = entry
	childReq.RepoAbs = remoteRepo
	childReq.Depth = depth - 1
	childReq.rpcSvcs = nil
	childReq.rpcByFunc = nil
	childReq.rpcFwd = nil
	childReq.fileCache = nil
	childReq.tgtsCache = nil
	childReq.extPkgs = nil
	childReq.ifaceImpls = nil
	childReq.diRegs = nil
	childReq.rpcRes = req.rpcRes
	childReq.httpRoutes = nil
	childReq.httpFiles = nil
	childReq.focus = nil
	childReq.Focus = nil
	childReq.sceneConsts = nil
	childReq.pkgSet = nil
	childReq.entryID = entry
	root, err := target.CodeSequence(childReq)
	if err != nil || root == nil {
		detail := "跨仓 HTTP handler 展开失败（" + remoteRepo + "）"
		if err != nil {
			detail += "：" + err.Error()
		}
		node.Stop = &SeqStop{Code: SeqStopHTTPTargetUnproven, Detail: detail}
		return true
	}
	node.Nodes = root.Nodes
	if root.Stop != nil && node.Stop == nil {
		node.Stop = root.Stop
	}
	return true
}

// seqExpandHTTPTarget 按已证明的 handler 展开（depth=1 保留身份 + 前沿）。
func seqExpandHTTPTarget(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	t seqHTTPTarget, depth int, curFile string, line int) {
	acts := a
	if t.acts != nil {
		acts = t.acts
	}
	if depth <= 1 {
		req.acc.depth = true
		node.Stop = &SeqStop{Code: SeqStopDepthLimit,
			Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开 HTTP handler 子树"}
		seqRegisterFrontier(req, node, t.handler, func(sub CodeSequenceRequest) {
			seqExpandHTTPTarget(a, sub, node, t, sub.Depth+1, curFile, line)
		}, node.Stop.Detail)
		return
	}
	if t.repo != "" && t.repo != req.RepoAbs {
		seqHTTPExpandRemote(acts, req, node, t.handler, t.repo, depth)
		return
	}
	if child, stop := codeSeqForSymbol(acts, req, t.handler, depth-1); child != nil {
		node.Nodes, node.Stop = child.Nodes, child.Stop
	} else if stop != nil {
		node.Stop = stop
	}
}
