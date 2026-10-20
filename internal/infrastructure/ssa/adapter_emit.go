package ssa

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"
	"sync"

	"github.com/schaepher/codeintel/internal/canonicalizer"
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// isModuleFunction 判断 SSA 函数是否属于项目内包。
func isModuleFunction(fn *ssa.Function, modules []string) bool {
	return fn.Pkg != nil && fn.Pkg.Pkg != nil && isInModule(fn.Pkg.Pkg.Path(), modules)
}

// emitFunction 发射单个函数的全部产出（Q174：局部收集）：
//  1. 函数/方法节点（Phase 1：保证边端点存在，ID 与 AST 适配器一致）
//  2. 字段访问节点与数据流边（Phase 2：field_extractor.go）
//  3. 返回 (ownerID, 局部 funcData)——由分块 worker pool 锁内合并进
//     data（闭包归外层；块间并行时同一 funcData 不再被并发写）
//
// 仅处理有 FuncDecl 源码的顶层函数/方法——闭包（FuncLit）与合成 wrapper 跳过；
// 闭包内字段访问在 Phase 2 归入外层函数（field_trace.md Q14 适配）。
func emitFunction(repo *domain.Repository, prog *ssa.Program, fn *ssa.Function,
	idents *identIndex, assignTargets []assignTarget,
	specs map[string]summarySpec, fallbackAgg *fallbackAgg, emit domain.EmitFunc,
	pool *implTypePool, lines *lineCache, funcs *funcSnapshot, dispatchRegs *dispatchReg,
	regHits regHits, typeMapping map[*types.Named]string) (domain.CanonicalID, *funcData, error) {
	logger := zap.L()
	logger.Debug("enter emitFunction")
	defer logger.Debug("exit emitFunction")
	if _, ok := fn.Syntax().(*ast.FuncDecl); !ok {
		// 闭包/合成函数：字段访问与 ORM 调用归外层函数（field_trace.md Q14）。
		// Q223：嵌套闭包（parent 也是闭包，Object 非 types.Func）此前直接
		// 跳过——内层闭包的字段访问/ORM 调用整块丢失；现向上找最外层具名
		// 函数。闭包参数无签名节点（emitSignatureNodes 只对顶层函数发射），
		// emitFunctionFields 传 sigEmitted=false，emitValue(Parameter) 自行
		// 发射（Q223 修复，Q222 同款漏报的闭包形态）。
		parent := fn.Parent()
		if parent == nil {
			return "", nil, nil
		}
		obj, ok := parent.Object().(*types.Func)
		if !ok || obj == nil {
			for p := parent.Parent(); p != nil; p = p.Parent() {
				if o, ok2 := p.Object().(*types.Func); ok2 && o != nil {
					obj = o
					ok = true
					break
				}
			}
			if !ok {
				return "", nil, nil
			}
		}
		pid, _, _ := funcIdentity(obj)
		if pid == "" {
			return "", nil, nil
		}
		fd := &funcData{}
		err := emitFunctionFields(repo, prog, fn, pid, idents, assignTargets, fd, specs, fallbackAgg, emit, pool, lines, funcs, dispatchRegs, regHits, typeMapping, false)
		return pid, fd, err
	}
	obj, ok := fn.Object().(*types.Func)
	if !ok || obj == nil {
		return "", nil, nil
	}
	pos := prog.Fset.PositionFor(fn.Pos(), false)
	filePath := relPath(repo.Path, pos.Filename)
	if filePath == "" {
		return "", nil, nil
	}
	id, kind, name := funcIdentity(obj)
	if id == "" {
		return "", nil, nil
	}
	n := &domain.CodeEntity{
		ID:        id,
		Kind:      kind,
		Name:      name,
		FilePath:  filePath,
		LineStart: pos.Line,
		LineEnd:   pos.Line,
		Properties: map[string]any{

			"signature": types.ObjectString(obj, types.RelativeTo(fn.Pkg.Pkg)),
		},
	}
	if err := emit(domain.Item{Node: n}); err != nil {
		return "", nil, err
	}

	if err := emitSignatureNodes(fn, id, pos, filePath, funcs, emit); err != nil {
		return "", nil, err
	}
	fd := &funcData{}
	err := emitFunctionFields(repo, prog, fn, id, idents, assignTargets, fd, specs, fallbackAgg, emit, pool, lines, funcs, dispatchRegs, regHits, typeMapping, true)
	return id, fd, err
}

// mergeFuncData 锁内合并局部 funcData 进共享 map（Q174 分块并发）：
// direct/indirect 条目均为 append 语义，合并顺序不影响结果集。
func mergeFuncData(fdMu *sync.Mutex, data map[domain.CanonicalID]*funcData,
	owner domain.CanonicalID, fd *funcData) {
	if owner == "" || fd == nil {
		return
	}
	if len(fd.directReads)+len(fd.directWrites)+len(fd.calls)+len(fd.indirectWrites) == 0 {
		return
	}
	fdMu.Lock()
	defer fdMu.Unlock()
	d := data[owner]
	if d == nil {
		// todo9-5：首次合并按来源长度预分配（多数函数只有一份贡献，
		// 直接定容避免 append 增长复制）
		d = &funcData{
			directReads:    make([]fieldEntry, 0, len(fd.directReads)),
			directWrites:   make([]fieldEntry, 0, len(fd.directWrites)),
			calls:          make([]callInfo, 0, len(fd.calls)),
			indirectWrites: make([]fieldEntry, 0, len(fd.indirectWrites)),
		}
		data[owner] = d
	}
	d.directReads = append(d.directReads, fd.directReads...)
	d.directWrites = append(d.directWrites, fd.directWrites...)
	d.calls = append(d.calls, fd.calls...)
	d.indirectWrites = append(d.indirectWrites, fd.indirectWrites...)
}

// funcIdentity 从 types.Func 生成 canonical ID / kind / name，与 AST 适配器 fnID 一致：
// 方法统一 (T).method（值/指针接收者不区分），匿名结构体上的方法返回空。

func funcIdentity(fn *types.Func) (domain.CanonicalID, domain.EntityKind, string) {
	logger := zap.L()
	logger.Debug("enter funcIdentity")
	defer logger.Debug("exit funcIdentity")
	if fn == nil || fn.Pkg() == nil {
		return "", "", ""
	}
	path := fn.Pkg().Path()
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil {
		return "", "", ""
	}
	if recv := sig.Recv(); recv != nil {
		t := recv.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		named, ok := t.(*types.Named)
		if !ok {
			return "", "", ""
		}
		name := canonicalizer.MethodName(named.Obj().Name(), fn.Name())
		return canonicalizer.GoSymbolID(path, name), domain.KindMethod, name
	}
	return canonicalizer.GoSymbolID(path, fn.Name()), domain.KindFunction, fn.Name()
}

// relPath 将绝对路径转为仓库相对路径；仓库外文件返回空串。
func relPath(repoPath, abs string) string {
	if abs == "" {
		return ""
	}
	rel, err := filepath.Rel(repoPath, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// isInModule 判断包路径是否属于任一被索引 module（自身或子包；P2-3
// 多 go.mod——任一 module 前缀匹配即项目内）。
func isInModule(pkgPath string, modules []string) bool {
	for _, m := range modules {
		if m == "" {
			continue
		}
		if pkgPath == m || strings.HasPrefix(pkgPath, m+"/") {
			return true
		}
	}
	return false
}
