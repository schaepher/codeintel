package action

// U04q：外部具体方法的实际声明 owner 与嵌入路径（method_path）。
//
// 自身直接声明优先；否则逐层检查嵌入类型（深度 8 / 每层 ≤64 路径）。同层
// 多路径、菱形重复到达同一 owner、具名字段遮蔽、嵌入接口、缺声明、循环
// 一律保守停止（unknown_receiver），不把唯一候选当实际 receiver。

import (
	"go/ast"
)

// 嵌入选择边界。
const (
	seqEmbedMaxDepth = 8
	seqEmbedMaxPaths = 64
)

// seqExternalMethodOwner 在 ref 类型（及其嵌入链）上查找方法 owner。
// 返回 (owner, methodPath, ambiguous, found)。
func seqExternalMethodOwner(a *Actions, req CodeSequenceRequest, ref seqORMTypeRef, method string,
	relFile string) (seqORMTypeRef, []string, bool, bool) {
	if ref.pkgPath == "" || ref.name == "" || method == "" {
		return seqORMTypeRef{}, nil, false, false
	}
	type node struct {
		ref  seqORMTypeRef
		path []string
	}
	start := node{ref: ref, path: []string{"symbol:go:" + ref.pkgPath + ":" + ref.name}}
	seen := map[string]bool{}
	level := []node{start}
	paths := 0
	for depth := 0; depth <= seqEmbedMaxDepth && len(level) > 0; depth++ {
		// U04q：同一层出现两条路径到达同一类型（菱形）也算歧义——不因 owner
		// 相同就去重成唯一实现。
		levelKeys := map[string]int{}
		for _, cur := range level {
			levelKeys[cur.ref.pkgPath+":"+cur.ref.name]++
		}
		for _, cnt := range levelKeys {
			if cnt > 1 {
				return seqORMTypeRef{}, nil, true, false
			}
		}
		var next []node
		var owners []node
		for _, cur := range level {
			key := cur.ref.pkgPath + ":" + cur.ref.name
			if seen[key] {
				continue
			}
			seen[key] = true
			paths++
			if paths > seqEmbedMaxPaths {
				return seqORMTypeRef{}, nil, true, false
			}
			st, iface, ok := seqTypeSpecOfRef(a, req, cur.ref, relFile)
			if !ok {
				if iface {
					return seqORMTypeRef{}, nil, true, false
				}
				continue
			}
			if seqPkgHasMethod(a, req, cur.ref, method, relFile) {
				owners = append(owners, cur)
				continue
			}
			if hasSeqNamedField(st, method) {
				// 具名字段与非匿名字段遮蔽方法提升。
				return seqORMTypeRef{}, nil, true, false
			}
			for _, emb := range seqEmbeddedTypes(st) {
				if emb.pkgPath == "" || emb.name == "" {
					continue
				}
				next = append(next, node{ref: emb,
					path: append(append([]string{}, cur.path...), "symbol:go:"+emb.pkgPath+":"+emb.name)})
			}
		}
		if len(owners) == 1 {
			return owners[0].ref, owners[0].path, false, true
		}
		if len(owners) > 1 {
			return seqORMTypeRef{}, nil, true, false
		}
		level = next
	}
	return seqORMTypeRef{}, nil, false, false
}

// seqPkgHasMethod 包内是否有接收者为 ref.name 的该方法声明。
func seqPkgHasMethod(a *Actions, req CodeSequenceRequest, ref seqORMTypeRef, method, relFile string) bool {
	for _, ctx := range seqPkgFilesForRef(a, req, ref.pkgPath, relFile) {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		for _, d := range ctx.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || fd.Name == nil {
				continue
			}
			if fd.Name.Name == method && recvTypeName(fd.Recv.List[0].Type) == ref.name {
				return true
			}
		}
	}
	return false
}

// seqTypeSpecRef 类型声明（外部/仓库内包）。
func seqTypeSpecOfRef(a *Actions, req CodeSequenceRequest, ref seqORMTypeRef,
	relFile string) (*seqStructTypeWrap, bool, bool) {
	files := seqPkgFilesForRef(a, req, ref.pkgPath, relFile)
	for _, ctx := range files {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		ts := fileTypeSpec(ctx.file, ref.name)
		if ts == nil {
			continue
		}
		// 别名跟随。
		if ts.Assign.IsValid() {
			c := &seqPersistCtx{file: ctx.file, pkgPath: ref.pkgPath, src: ctx.src, fset: ctx.fset,
				actions: a, repoAbs: req.RepoAbs, fileRel: ctx.rel, req: &req, ormModels: req.ormModels}
			next, ok := seqORMTypeExprRef(c, ts.Type, ctx.file, ref.pkgPath, 0)
			if !ok {
				return nil, false, false
			}
			return seqTypeSpecOfRef(a, req, next, ctx.rel)
		}
		if _, isIface := ts.Type.(*ast.InterfaceType); isIface {
			return nil, true, false
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return nil, false, false
		}
		return &seqStructTypeWrap{fields: st.Fields, file: ctx}, false, true
	}
	return nil, false, false
}

// seqStructTypeWrap 结构体字段列表 + 声明文件（import 上下文）。
type seqStructTypeWrap struct {
	fields *ast.FieldList
	file   *seqFileCtx
}

// StructType 便捷方法（保持调用点简洁）。
func (w *seqStructTypeWrap) StructType() (*seqStructTypeWrap, bool) { return w, w != nil }

// hasSeqNamedField 字段是否与目标方法同名（遮蔽提升）——具名字段与非匿名
// 同名字段都不穿透。
func hasSeqNamedField(st *seqStructTypeWrap, method string) bool {
	if st == nil || st.fields == nil {
		return false
	}
	for _, fl := range st.fields.List {
		if len(fl.Names) == 0 {
			t := fl.Type
			if star, ok := t.(*ast.StarExpr); ok {
				t = star.X
			}
			if exprTypeName(t) == method {
				return true
			}
			continue
		}
		for _, n := range fl.Names {
			if n.Name == method {
				return true
			}
		}
	}
	return false
}

// seqEmbeddedTypes 结构体的匿名嵌入字段类型（指针/别名展开一层）。
func seqEmbeddedTypes(st *seqStructTypeWrap) []seqORMTypeRef {
	if st == nil || st.fields == nil {
		return nil
	}
	c := &seqPersistCtx{file: st.file.file, pkgPath: pkgPathOfFile(st.file), src: st.file.src,
		fset: st.file.fset}
	var out []seqORMTypeRef
	for _, fl := range st.fields.List {
		if len(fl.Names) != 0 {
			continue
		}
		t := fl.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if ref, ok := seqORMTypeExprRef(c, t, st.file.file, c.pkgPath, 0); ok {
			out = append(out, ref)
		}
	}
	return out
}

// pkgPathOfFile 文件所属包路径（外部 <external>/ 前缀的按证据前缀还原）。
func pkgPathOfFile(ctx *seqFileCtx) string {
	if ctx == nil {
		return ""
	}
	return ctx.pkgPath
}
