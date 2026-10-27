package action

// todo22 §2.4/§5：函数列表的 range 展开、range 多候选诊断与包内常量表。

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// seqConstValues 当前包（同目录 .go 文件）的常量表：name → 值文本。
// todo22 §5：场景判定要能比较整型/有类型常量（Source=7 vs const SourceA=7），
// 值来自真实声明（含 `Source(7)` 这种转换），不按名称猜。
//
// todo22 P1：返回带**跨包按需解析**的常量表（`otherpkg.Const`）——见
// action_scene_const.go；当前包常量仍然一次建表（与旧实现等价）。
func seqConstValues(a *Actions, req CodeSequenceRequest, relFile string) *seqConstTable {
	consts := map[string]string{}
	tbl := &seqConstTable{vals: consts, a: a, req: req, ctx: seqFileCtxFor(req, relFile)}
	dir := filepath.Dir(filepath.Join(req.RepoAbs, relFile))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return tbl
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
			continue
		}
		fileRel := filepath.Join(filepath.Dir(relFile), ent.Name())
		if filepath.Dir(relFile) == "." {
			fileRel = ent.Name()
		}
		ctx := seqFileCtxFor(req, fileRel)
		if ctx == nil {
			continue
		}
		// 常量表达式里的跨包引用按**该文件**的 import 别名解析
		fileTbl := &seqConstTable{vals: consts, a: a, req: req, ctx: ctx}
		for _, decl := range ctx.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if v, ok := sceneValueOf(vs.Values[i], fileTbl); ok {
						consts[name.Name] = v
					} else if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
						consts[name.Name] = strings.Trim(lit.Value, "\"`")
					}
				}
			}
		}
	}
	return tbl
}

// seqRangeAmbiguous range 对象是函数值的多候选（多个互斥列表）时的诊断。
func seqRangeAmbiguous(req CodeSequenceRequest, x ast.Expr) string {
	if req.env == nil {
		return ""
	}
	id, ok := x.(*ast.Ident)
	if !ok {
		return ""
	}
	v, ok := req.env.lookup(id.Obj)
	if !ok || v.kind != seqValueMulti {
		return ""
	}
	return "range 对象有 " + itoaSeq(len(v.cands)) + " 个候选列表（条件覆盖未消歧），不按顺序展开"
}
