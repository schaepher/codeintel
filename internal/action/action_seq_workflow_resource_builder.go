package action

// 资源改写事件遍历器（从 action_seq_workflow_resource.go 拆出）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// seqParsedGoFile 解析后的包内文件。
type seqParsedGoFile struct {
	rel  string
	file *ast.File
	fset *token.FileSet
}

// seqParsePackageDir 解析目录内所有 Go 文件（含跨文件包级声明）。
func seqParsePackageDir(dir string) map[string]*seqParsedGoFile {
	out := map[string]*seqParsedGoFile{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			continue
		}
		out[e.Name()] = &seqParsedGoFile{rel: e.Name(), file: f, fset: fset}
	}
	return out
}

// seqResourceFactBuilder 语句顺序遍历（入口用真实位置，被调链用调用点位置）。
type seqResourceFactBuilder struct {
	out        *seqEntryFactData
	pkg        string
	files      map[string]*seqParsedGoFile
	pkgVars    map[string]bool
	pkgVarObjs map[*ast.Object]bool
	repo       string
	entry      string
	fileDir    string // 入口文件所在包目录（仓库相对；来源键前缀）
	// log 事件插入顺序（块终止时按区间回填 die 边界）。
	log []seqResEventRef
}

// seqResEventRef 已记录事件的定位（资源符号 + 该符号事件切片下标）。
type seqResEventRef struct {
	name string
	idx  int
}

// walkFunc 遍历函数体；eventAt>0 表示被调链（所有事件记在调用点）。
func (b *seqResourceFactBuilder) walkFunc(fn *ast.FuncDecl, file *seqParsedGoFile,
	eventAt int, isEntry bool, depth int) {
	if fn == nil || fn.Body == nil {
		return
	}
	if depth > seqResourceFactMaxDepth {
		b.out.partial = true
		return
	}
	b.walkBlock(fn.Body, file, eventAt, isEntry, depth, map[*ast.Object]*ast.FuncLit{})
}

// walkBlock 顺序遍历语句块。
//
// U04bU：块内改写事件在**块终止**（return/panic 分支）时打上 die 边界——
// 已返回路径的改写不能污染继续执行的读取；未终止块保持无边界（保守）。
func (b *seqResourceFactBuilder) walkBlock(body *ast.BlockStmt, file *seqParsedGoFile,
	eventAt int, isEntry bool, depth int, closures map[*ast.Object]*ast.FuncLit) {
	if body == nil {
		return
	}
	mark := len(b.log)
	for _, st := range body.List {
		b.walkStmt(st, file, eventAt, isEntry, depth, closures)
	}
	if isEntry && seqBlockTerminates(body) && file.fset != nil && body.Rbrace.IsValid() {
		die := file.fset.Position(body.Rbrace).Offset
		for _, ref := range b.log[mark:] {
			ev := &b.out.events[ref.name][ref.idx]
			if ev.die == 0 || die < ev.die {
				ev.die = die
			}
		}
	}
}

// walkStmt 单条语句：资源赋值 + 调用/闭包（按源码顺序）。
func (b *seqResourceFactBuilder) walkStmt(st ast.Stmt, file *seqParsedGoFile,
	eventAt int, isEntry bool, depth int, closures map[*ast.Object]*ast.FuncLit) {
	pos := eventAt
	if isEntry {
		pos = file.fset.Position(st.Pos()).Offset
	}
	switch s := st.(type) {
	case *ast.AssignStmt:
		// 记录闭包变量（未调用不执行）。
		for i, lhs := range s.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || i >= len(s.Rhs) {
				continue
			}
			if lit, ok2 := runtimeUnparen(s.Rhs[i]).(*ast.FuncLit); ok2 && id.Obj != nil {
				closures[id.Obj] = lit
			}
		}
		for _, lhs := range s.Lhs {
			if id, ok := runtimeUnparen(lhs).(*ast.Ident); ok && b.isPkgVar(id) {
				b.addEvent(id.Name, pos)
			}
		}
		for _, rhs := range s.Rhs {
			b.walkExprCalls(rhs, file, pos, isEntry, depth, closures)
		}
	case *ast.IncDecStmt:
		if id, ok := runtimeUnparen(s.X).(*ast.Ident); ok && b.isPkgVar(id) {
			b.addEvent(id.Name, pos)
		}
	case *ast.ExprStmt:
		b.walkExprCalls(s.X, file, pos, isEntry, depth, closures)
	case *ast.IfStmt:
		if s.Init != nil {
			b.walkStmt(s.Init, file, eventAt, isEntry, depth, closures)
		}
		switch seqStaticCond(s.Cond) {
		case 1: // 恒真：Else 不可达
			b.walkBlock(s.Body, file, eventAt, isEntry, depth, closures)
		case -1: // 恒假：Then 不可达
			if s.Else != nil {
				b.walkStmt(s.Else, file, eventAt, isEntry, depth, closures)
			}
		default:
			b.walkBlock(s.Body, file, eventAt, isEntry, depth, closures)
			if s.Else != nil {
				b.walkStmt(s.Else, file, eventAt, isEntry, depth, closures)
			}
		}
	case *ast.ForStmt:
		// U04bU：Init 无条件执行一次；静态 false 的循环体（及 Post）零次执行。
		if s.Init != nil {
			b.walkStmt(s.Init, file, eventAt, isEntry, depth, closures)
		}
		if s.Cond != nil && seqStaticCond(s.Cond) == -1 {
			return
		}
		b.walkBlock(s.Body, file, eventAt, isEntry, depth, closures)
		// Post 每次迭代后执行（≥1 次）；不改写被跳过。
		if s.Post != nil {
			b.walkStmt(s.Post, file, eventAt, isEntry, depth, closures)
		}
	case *ast.RangeStmt:
		b.walkBlock(s.Body, file, eventAt, isEntry, depth, closures)
	case *ast.SwitchStmt:
		b.walkBlock(s.Body, file, eventAt, isEntry, depth, closures)
	case *ast.BlockStmt:
		b.walkBlock(s, file, eventAt, isEntry, depth, closures)
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			b.walkExprCalls(r, file, pos, isEntry, depth, closures)
		}
	case *ast.DeclStmt:
		b.walkDeclClosures(s, file, pos, isEntry, depth, closures)
	case *ast.DeferStmt:
		// defer 的实参立即求值，函数体延迟执行（资源改写不在此刻发生）。
		b.walkExprCalls(s.Call, file, pos, isEntry, depth, closures)
	case *ast.GoStmt:
		b.walkExprCalls(s.Call, file, pos, isEntry, depth, closures)
	}
}

// walkExprCalls 表达式内调用（按源码顺序）：处理 IIFE/本地函数/闭包变量。
func (b *seqResourceFactBuilder) walkExprCalls(e ast.Expr, file *seqParsedGoFile,
	pos int, isEntry bool, depth int, closures map[*ast.Object]*ast.FuncLit) {
	if e == nil {
		return
	}
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		b.applyCall(call, file, pos, depth, closures)
		return true
	})
}

// applyCall 调用点发生的资源改写（递归被调/闭包/仓内跨包函数）。
func (b *seqResourceFactBuilder) applyCall(call *ast.CallExpr, file *seqParsedGoFile,
	pos int, depth int, closures map[*ast.Object]*ast.FuncLit) {
	if call == nil {
		return
	}
	switch fun := call.Fun.(type) {
	case *ast.FuncLit:
		b.walkBlock(fun.Body, file, pos, false, depth+1, closures)
	case *ast.Ident:
		if lit, ok := closures[fun.Obj]; ok {
			b.walkBlock(lit.Body, file, pos, false, depth+1, closures)
			return
		}
		if fd := b.pkgFunc(fun.Name); fd != nil {
			b.walkFuncIn(fd, b.fileOfFunc(fd), pos, depth+1)
		}
	case *ast.SelectorExpr:
		pkgID, ok := fun.X.(*ast.Ident)
		if !ok || pkgID.Obj != nil {
			return
		}
		if importPath := seqImportPath(file.file, pkgID.Name); importPath != "" {
			if fd, f := b.importFunc(importPath, fun.Sel.Name); fd != nil {
				b.walkFuncIn(fd, f, pos, depth+1)
			}
		}
	}
}

// walkFuncIn 被调函数体（事件记调用点位置）。
func (b *seqResourceFactBuilder) walkFuncIn(fn *ast.FuncDecl, file *seqParsedGoFile,
	callOffset, depth int) {
	if fn == nil || file == nil {
		return
	}
	if depth > seqResourceFactMaxDepth {
		b.out.partial = true
		return
	}
	b.walkBlock(fn.Body, file, callOffset, false, depth, map[*ast.Object]*ast.FuncLit{})
}

// pkgFunc 同包函数（唯一同名）。
func (b *seqResourceFactBuilder) pkgFunc(name string) *ast.FuncDecl {
	var found *ast.FuncDecl
	for _, f := range b.files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != name {
				continue
			}
			if found != nil {
				return nil
			}
			found = fd
		}
	}
	return found
}

// addEvent 记录一次资源改写事件。
func (b *seqResourceFactBuilder) addEvent(name string, offset int) {
	if b == nil || b.out == nil || name == "" {
		return
	}
	sym := "symbol:go:" + b.pkg + ":" + name
	b.out.events[sym] = append(b.out.events[sym], seqResEvent{off: offset})
	b.log = append(b.log, seqResEventRef{name: sym, idx: len(b.out.events[sym]) - 1})
}

// isPkgVar 赋值目标是否指向包级 var（AST 声明身份；局部同名不命中）。
func (b *seqResourceFactBuilder) isPkgVar(id *ast.Ident) bool {
	if id == nil {
		return false
	}
	if id.Obj != nil {
		return b.pkgVarObjs[id.Obj]
	}
	// 跨文件引用在单文件 parser 里没有 Obj：只能按名称回退。
	return b.pkgVars[id.Name]
}
