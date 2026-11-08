package action

// B06e.4：事务写入的底层资源（Begin 接收者 → 包级 DB 声明）。
//
// 从 action_seq_workflow_conn.go 拆出（行数治理）；跨包 helper 的形参
// 接收者按真实调用实参回到入口包的包级声明，不把形参名当资源。

import (
	"go/ast"
	"path/filepath"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqWFResolveTxResource 事务 Begin 调用接收者的包级 DB 声明符号。
//
// 跨文件引用在单文件 parser 里没有 ast.Object——按**包级 var 名称表**回退
// （局部同名变量仍不命中，因为只有包级声明在表里）。
func seqWFResolveTxResource(entries []SeqWorkflowEntry, entry, txID, repo string) string {
	tree := seqWFEntryTree(entries, entry)
	if tree == nil {
		return ""
	}
	var pkgVars map[string]bool
	var found string
	var walk func(nodes []*CodeSeqNode, parent *CodeSeqNode)
	walk = func(nodes []*CodeSeqNode, parent *CodeSeqNode) {
		for _, n := range nodes {
			if found != "" || n == nil {
				continue
			}
			tx := n.Evidence
			if tx != nil && tx.Transaction != nil && tx.Transaction.Origin != nil &&
				tx.Transaction.Origin.ID == txID && tx.Transaction.Action == SeqTxBegin &&
				n.call != nil {
				if sel, ok := n.call.Fun.(*ast.SelectorExpr); ok {
					if id, ok2 := runtimeUnparen(sel.X).(*ast.Ident); ok2 {
						if id.Obj != nil {
							// B06e.4：跨包 helper 的 Begin 接收者是形参——按
							// 真实返回槽回到调用实参（DB 声明身份），不能把
							// 形参名当成入口包的包级资源。
							found = seqWFTxReceiverResource(repo, entry, tx.CallFile,
								tx.CallLine, id.Name, parent, pkgVars)
						} else {
							if pkgVars == nil {
								dir := filepath.Dir(filepath.Join(repo, tx.CallFile))
								pkgVars = seqPkgVarNames(seqParsePackageDir(dir))
							}
							if pkgVars[id.Name] {
								found = "symbol:go:" + pkgOfID(domain.CanonicalID(entry)) + ":" + id.Name
							}
						}
					}
				}
			}
			walk(n.Nodes, n)
			walk(n.Else, parent)
		}
	}
	walk(tree.Nodes, nil)
	return found
}

// seqWFTxReceiverResource Begin 接收者标识符 → 包级资源符号。
//
// 包级 var（Obj==nil 的跨文件引用/名称表）直接取值；形参则用所在 helper
// 的形参位置回到父调用点的实参，仅当实参是入口包的包级 var 时成立。
func seqWFTxReceiverResource(repo, entry, callFile string, callLine int, name string,
	parent *CodeSeqNode, pkgVars map[string]bool) string {
	entryPkg := pkgOfID(domain.CanonicalID(entry))
	if parent != nil && parent.call != nil {
		if idx := seqHelperParamIndex(repo, callFile, callLine, name); idx >= 0 &&
			idx < len(parent.call.Args) {
			if id, ok := runtimeUnparen(parent.call.Args[idx]).(*ast.Ident); ok {
				dir := filepath.Dir(filepath.Join(repo, parent.Evidence.CallFile))
				if entryPkgVars := seqPkgVarNames(seqParsePackageDir(dir)); entryPkgVars[id.Name] {
					return "symbol:go:" + entryPkg + ":" + id.Name
				}
			}
			return ""
		}
	}
	if pkgVars == nil {
		pkgVars = seqPkgVarNames(seqParsePackageDir(filepath.Dir(filepath.Join(repo, callFile))))
	}
	if pkgVars[name] {
		return "symbol:go:" + entryPkg + ":" + name
	}
	return ""
}

// seqHelperParamIndex 文件里包含 line 的函数声明中形参 name 的位置（0 起）。
func seqHelperParamIndex(repo, rel string, line int, name string) int {
	files := seqParsePackageDir(filepath.Dir(filepath.Join(repo, rel)))
	f := files[filepath.Base(rel)]
	if f == nil || f.file == nil || f.fset == nil {
		return -1
	}
	for _, d := range f.file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Type == nil || fd.Body == nil {
			continue
		}
		start := f.fset.Position(fd.Pos()).Line
		end := f.fset.Position(fd.End()).Line
		if line < start || line > end {
			continue
		}
		idx := 0
		for _, field := range fd.Type.Params.List {
			for _, n := range field.Names {
				if n.Name == name {
					return idx
				}
				idx++
			}
			if len(field.Names) == 0 {
				idx++
			}
		}
		return -1
	}
	return -1
}
