package ssa

// todo10-3：源码标识符索引改为**按文件惰性构建**（原实现建全局
// map[token.Pos]string，go2o 约 26MB 分配且只被少量 recoverVarName/
// instancePath 查询使用）。
//
// 等价性：索引数据源与遍历方式不变（模块包 Syntax 的 ast.Ident），
// 只是从"一次全量"变为"首次查询某文件时构建该文件"——同一 Pos 的
// 命中/名称完全一致；非模块文件/NoPos 仍查不到。

import (
	"go/ast"
	"go/token"
	"sort"
	"sync"

	"golang.org/x/tools/go/packages"
)

// identIndex 源码标识符索引（并发安全；按**声明**惰性建表）。
// 查询位置都来自 SSA 值（函数体内的变量/phi 声明），按包含该位置的
// FuncDecl/GenDecl 建表即可；包名标识符单独短路。
type identIndex struct {
	fset    *token.FileSet
	files   map[*token.File]*ast.File
	ordered []*ast.File // 与旧 buildIdentIndex 相同的遍历顺序（pkgs→Syntax）
	mu      sync.Mutex
	byDecl  map[ast.Node]map[token.Pos]string
	// NoPos 兼容：旧全局 map 若包含 Pos==0 的标识符（go2o 实测 1 个
	// 名字为空的合成 Ident），则任何 Pos==0 的查询都会命中它。惰性扫描
	// （首次 NoPos 查询时）复刻该行为——只保留"最后一个"名字（旧 map
	// 后写覆盖）。
	noPosOnce sync.Once
	noPosVal  string
	noPosOK   bool
}

// lookup 查询位置上的标识符名（非标识符/未索引文件返回 false）。
func (ix *identIndex) lookup(pos token.Pos) (string, bool) {
	if ix == nil || ix.fset == nil {
		return "", false
	}
	if pos == token.NoPos {
		ix.noPosOnce.Do(func() { ix.noPosVal, ix.noPosOK = scanNoPosIdent(ix.ordered) })
		return ix.noPosVal, ix.noPosOK
	}
	f := ix.fset.File(pos)
	if f == nil {
		return "", false
	}
	file := ix.files[f]
	if file == nil {
		return "", false
	}
	if file.Name != nil && file.Name.Pos() == pos {
		return file.Name.Name, true
	}
	name, ok := ix.declMap(file, pos)[pos]
	return name, ok
}

// declMap 取（必要时构建）包含 pos 的声明的 Pos→标识符名表。
func (ix *identIndex) declMap(file *ast.File, pos token.Pos) map[token.Pos]string {
	decls := file.Decls
	i := sort.Search(len(decls), func(i int) bool { return decls[i].Pos() > pos }) - 1
	if i < 0 || pos >= decls[i].End() {
		return nil
	}
	decl := decls[i]
	ix.mu.Lock()
	m, ok := ix.byDecl[decl]
	ix.mu.Unlock()
	if ok {
		return m
	}
	m = map[token.Pos]string{}
	ast.Inspect(decl, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			m[id.Pos()] = id.Name
		}
		return true
	})
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if prev, ok := ix.byDecl[decl]; ok {
		return prev
	}
	if ix.byDecl == nil {
		ix.byDecl = map[ast.Node]map[token.Pos]string{}
	}
	ix.byDecl[decl] = m
	return m
}

// scanNoPosIdent 按旧 map 构建顺序扫描 Pos==0 的标识符（后写覆盖）。
func scanNoPosIdent(files []*ast.File) (string, bool) {
	var name string
	var ok bool
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if id, isID := n.(*ast.Ident); isID && id.Pos() == token.NoPos {
				name, ok = id.Name, true
			}
			return true
		})
	}
	return name, ok
}

// buildIdentIndex 建立模块包的惰性标识符索引（只登记文件，不遍历 AST）。
func buildIdentIndex(pkgs []*packages.Package, modules []string) *identIndex {
	ix := &identIndex{}
	for _, p := range pkgs {
		if !isInModule(p.PkgPath, modules) || p.Fset == nil {
			continue
		}
		if ix.fset == nil {
			ix.fset = p.Fset // Q251：多 module 共享同一 FileSet
		}
		for _, f := range p.Syntax {
			ix.ordered = append(ix.ordered, f)
			file := p.Fset.File(f.Pos())
			if file == nil {
				continue
			}
			if ix.files == nil {
				ix.files = map[*token.File]*ast.File{}
			}
			ix.files[file] = f
		}
	}
	return ix
}
