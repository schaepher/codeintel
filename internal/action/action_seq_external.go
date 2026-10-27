package action

// todo22 §4.2：外部依赖的**按需源码解析**（离线、只读、请求内缓存）。
//
// 约束（用户/规范一致）：
//   - 只在**实际被调用**且 depth>1 时才解析；depth<=1 只保留准确外部目标与
//     depth 诊断，不启动 go list（不为不需要的展开付代价）。
//   - `go list -e -json -find -mod=readonly` + `GOPROXY=off` / `GOSUMDB=off`
//     / `GOFLAGS=-mod=readonly`：不下载、不访问 sumdb、不改写 go.mod/go.sum；
//     支持 go.mod 的 local replace 与仓库内嵌套依赖模块。
//   - 成功与失败都按 import path 缓存（同一请求不重复起子进程）。
//   - 进入外部函数后按**它自己的形参**重新绑定实参（回调/函数列表/ellipsis），
//     不沿用调用者的同名参数映射；只有外部实现真的调用了形参，回调才算
//     已证明执行。
//   - 取消、过滤、停止包、深度规则与普通调用共用（ctx 一路传下去）。

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// SeqEvidenceExternal 外部依赖源码（本地模块/replace/Go module cache）。
const SeqEvidenceExternal = "external_source"

// seqExternalPkg 一个外部包的解析结果（成功或失败都缓存）。
type seqExternalPkg struct {
	dir   string
	std   bool // 标准库包（GOROOT）：不作为"外部依赖"展开（避免 stdlib 噪声）
	err   error
	files []*seqFileCtx // 已解析的 .go 文件
	rel   string        // 证据里的路径前缀（<外部>/<import path>）
}

// seqExternalPackage 解析 import path → 包目录与源码（带缓存）。
func seqExternalPackage(a *Actions, req CodeSequenceRequest, importPath string) *seqExternalPkg {
	if req.extPkgs != nil {
		if p, ok := req.extPkgs[importPath]; ok {
			return p
		}
	}
	p := &seqExternalPkg{rel: "<external>/" + importPath}
	p.dir, p.std, p.err = seqGoListPkg(req.ctx(), req.RepoAbs, importPath)
	if p.err == nil && p.dir != "" && !p.std {
		p.files = parseSeqDir(p.dir, p.rel)
	}
	if req.extPkgs != nil {
		req.extPkgs[importPath] = p
	}
	return p
}

// seqGoListPkg `go list -e -json -find -mod=readonly <importPath>`（离线只读）。
// 成功返回 package Dir + 是否标准库；失败返回原因（查询阶段不构建索引、
// 不下载依赖）。
func seqGoListPkg(ctx context.Context, workDir, importPath string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-json", "-find", "-mod=readonly", importPath)
	cmd.Dir = workDir
	// 离线：禁止下载与 sumdb 访问；只读：不修改 go.mod/go.sum
	cmd.Env = append(seqFilteredEnv(os.Environ(), "GOFLAGS", "GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK", "GOPRIVATE"),
		"GOFLAGS=-mod=readonly", "GOPROXY=off", "GOSUMDB=off", "GONOSUMDB=*", "GONOSUMCHECK=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", false, &seqExternalError{Import: importPath, Err: err.Error(), Stderr: strings.TrimSpace(stderr.String())}
	}
	var out struct {
		Dir      string
		Standard bool
		Error    *struct{ Err string }
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return "", false, &seqExternalError{Import: importPath, Err: "解析 go list 输出失败: " + err.Error()}
	}
	if out.Error != nil && out.Error.Err != "" {
		return "", false, &seqExternalError{Import: importPath, Err: out.Error.Err}
	}
	if out.Dir == "" {
		return "", false, &seqExternalError{Import: importPath, Err: "go list 未返回包目录（外部依赖不可定位）"}
	}
	return out.Dir, out.Standard, nil
}

// seqExternalError 外部包定位失败（错误文本可读，缓存后不重复尝试）。
type seqExternalError struct {
	Import string
	Err    string
	Stderr string
}

func (e *seqExternalError) Error() string {
	msg := "外部包 " + e.Import + " 源码不可定位: " + e.Err
	if e.Stderr != "" {
		msg += "（go list: " + e.Stderr + "）"
	}
	return msg
}

// seqFilteredEnv 去掉指定环境变量后返回（避免与追加的离线值冲突）。
func seqFilteredEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		skip := false
		for _, k := range keys {
			if name == k {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	return out
}

// parseSeqDir 解析目录下的非测试 .go 文件（外部包没有索引，只能读源码）。
func parseSeqDir(dir, relPrefix string) []*seqFileCtx {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*seqFileCtx
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		full := filepath.Join(dir, name)
		src, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, full, src, 0)
		if err != nil {
			continue
		}
		out = append(out, &seqFileCtx{fset: fset, file: f, src: src, rel: path.Join(relPrefix, name)})
	}
	return out
}

// seqFindExternalDecl 在外部包源码里找函数声明（名字 + receiver 精确匹配）。
func seqFindExternalDecl(pkg *seqExternalPkg, declName string) (*seqFileCtx, *ast.FuncDecl) {
	recvType, funcName, isMethod := splitDeclName(declName)
	for _, ctx := range pkg.files {
		for _, decl := range ctx.file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name == nil || fd.Name.Name != funcName {
				continue
			}
			if isMethod {
				if fd.Recv == nil || len(fd.Recv.List) == 0 ||
					recvTypeName(fd.Recv.List[0].Type) != recvType {
					continue
				}
			} else if fd.Recv != nil {
				continue
			}
			return ctx, fd
		}
	}
	return nil, nil
}

// seqExternalCallTarget 未解析调用 → 外部包目标（包级函数 pkg.Fn）。
// 返回 (import path, 声明名, 准确 canonical ID)。
func seqExternalCallTarget(f *ast.File, fun ast.Expr) (string, string, string, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || (id.Obj != nil && id.Obj.Kind != ast.Pkg) {
		return "", "", "", false
	}
	importPath := seqImportPath(f, id.Name)
	if importPath == "" {
		return "", "", "", false
	}
	name := sel.Sel.Name
	return importPath, name, "symbol:go:" + importPath + ":" + name, true
}

// seqExpandExternalCall 按需展开外部依赖函数体。
// expanded=false 表示没有展开（调用方继续走未解析分类）。
func seqExpandExternalCall(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	f *ast.File, fun ast.Expr, call *ast.CallExpr, depth int, curFile string, line int) bool {
	importPath, declName, extID, ok := seqExternalCallTarget(f, fun)
	if !ok {
		return false
	}
	if req.pkgSet[importPath] {
		return false // 本仓包（交给索引/接口派发路径）
	}
	// 准确目标身份先写上（depth 截断也要保留外部目标 ID）
	node.Evidence = &SeqEvidence{Source: SeqEvidenceAST, CallFile: curFile, CallLine: line,
		SymbolID: extID, Reason: "external-target"}
	if depth <= 1 {
		// depth=1：保留准确外部目标 + depth 诊断，**不启动 go list**
		req.acc.depth = true
		node.Stop = &SeqStop{Code: SeqStopDepthLimit,
			Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开外部依赖源码: " + extID}
		return true
	}
	if err := req.ctx().Err(); err != nil {
		req.acc.cancel = true
		return true
	}
	pkg := seqExternalPackage(a, req, importPath)
	if pkg.err == nil && pkg.std {
		// 标准库不按“外部依赖”展开（噪音），保持 external_target 分类
		return false
	}
	if pkg.err != nil {
		node.Stop = &SeqStop{Code: SeqStopExternalTarget, Detail: pkg.err.Error()}
		return true
	}
	ctx, fd := seqFindExternalDecl(pkg, declName)
	if ctx == nil || fd == nil || fd.Body == nil {
		node.Stop = &SeqStop{Code: SeqStopExternalTarget,
			Detail: "外部包源码里找不到 " + declName + "（" + extID + "）"}
		return true
	}
	// 外部函数进入：按它自己的形参绑定实参（不沿用调用者同名参数映射）
	sub := req
	sub.env = newSeqEnv(nil)
	bindSeqParams(sub.env, fd.Type.Params, captureSeqClosureArgs(a, req, req.curCtx, call))
	sub.curSym = extID
	sub.curCtx = ctx
	sub.curFile = ctx.rel
	sub.curTgts = &seqTargets{} // 外部包没有本仓索引边；回调靠参数绑定证明
	sub.curRecvType = ""
	sub.curRecvDecl = nil
	sub.sceneConsts = nil
	node.Evidence = &SeqEvidence{Source: SeqEvidenceExternal, CallFile: curFile, CallLine: line,
		SymbolID: extID, File: ctx.rel, Line: ctx.fset.Position(fd.Pos()).Line,
		Reason: "external-source"}
	node.Nodes = walkStmts(a, sub, ctx.fset, ctx.src, fd.Body.List, sub.curTgts, depth,
		"", nil, ctx.file, pkgPathOf(extID), ctx.rel)
	if len(node.Nodes) == 0 {
		node.Stop = &SeqStop{Code: SeqStopNoCalls}
	}
	return true
}
