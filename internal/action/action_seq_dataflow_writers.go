package action

// U06：receiver 字段写入来源的数据流（从 action_seq_dataflow.go 拆出——
// 行数治理）。写入来源未知（形参/未知调用）时必须给出 unknown 哨兵，不能
// 因为还有其它具体写者就当唯一实例来源。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// receiverFieldImpl 数据流具体化（R97-2 + P0-3）：receiver 字段
// （s.manager）的赋值来源类型——查摘要 direct_write 写入函数 → 读源码
// AST 找赋值表达式（x.manager = ...）的类型来源（字面量/构造器/变量
// 链/条件分支多候选）→ 构造 (Impl).Method。找不到（无写入摘要/形态
// 不支持）返回空——调用方 fallback 接口类型匹配（InterfaceMethodImpl）。
func (a *Actions) receiverFieldImpl(req CodeSequenceRequest, recvType, field, method string) string {
	if impls, _ := a.receiverFieldImplsEx(req, recvType, field, method); len(impls) > 0 {
		return impls[0]
	}
	return ""
}

// receiverFieldImpls 字段写入来源的**全部**实现方法（多个写者/多个
// 具体类型 → 调用方必须保留候选，不能取第一个当唯一证据）。
func (a *Actions) receiverFieldImpls(req CodeSequenceRequest, recvType, field, method string) []string {
	impls, _ := a.receiverFieldImplsEx(req, recvType, field, method)
	return impls
}

// receiverFieldImplsEx 同 receiverFieldImpls，并返回写入来源是否**不完整**
// （U06：形参/未知方法等无法证明的写者存在）。unknown=true 时调用方必须
// 放弃"唯一实现"结论——唯一实现不等于实际实例来源。
func (a *Actions) receiverFieldImplsEx(req CodeSequenceRequest, recvType, field, method string) (impls []string, unknown bool) {
	key := "." + recvType + "." + field
	writers, cached := req.fieldWriters[key]
	if !cached {
		var err error
		writers, err = a.repo.GetFieldWriters(key)
		if err != nil {
			writers = nil
		}
		if len(writers) == 0 {
			// 索引里的 field_path 是 SSA 的 pkg.Type.field 全路径（真实 init 的
			// 普通形态）；先精确查短路径，未命中再按类型/字段后缀补查。
			if ws, serr := a.repo.GetFieldWritersBySuffix(key); serr == nil {
				writers = ws
			}
		}
		if req.fieldWriters != nil {
			req.fieldWriters[key] = writers // 命中/未命中都缓存（请求内）
		}
	}
	if len(writers) == 0 {
		return nil, false
	}
	var out []string
	for _, w := range writers {
		srcs := a.fieldWriteTypes(req, w, field)
		if hasUnknownWriteSource(srcs) {
			unknown = true
		}
		for _, c := range knownWriteSources(srcs) {
			id := GrpcMethodEntryID("symbol:go:"+c.pkgPath+":"+c.typeName, method)
			if id == "" {
				continue
			}
			dup := false
			for _, e := range out {
				if e == id {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, id)
			}
		}
	}
	return out, unknown
}

// fieldWriteTypes 读写入函数源码，找 "x.<field> = <X>" 赋值中 X 的类型
// 来源（P0-3 扩展——R97-2 仅 &T{} 字面量）：
//   - 字面量：&T{} / T{}（Ident 同包；SelectorExpr pkg.T 跨包——import
//     映射解析包路径）
//   - 构造器调用：newX() / pkg.NewX() → 函数返回类型（优先函数体
//     return RHS——"声明返回接口、函数体 return 具体实现"形态；跨包
//     经索引节点读源码）
//   - 变量链：x.<field> = v → 向上回溯 v 的初始化赋值（深度 ≤3 防环）
//
// 条件分支各分支的赋值都收集（多候选——去重后返回）。
func (a *Actions) fieldWriteTypes(req CodeSequenceRequest, writerID, field string) []writeSource {
	cacheKey := writerID + "\x00" + field
	if v, ok := req.fieldTypes[cacheKey]; ok {
		return v
	}
	n, err := a.repo.GetSymbol(domain.CanonicalID(writerID))
	if err != nil || n.FilePath == "" {
		if req.fieldTypes != nil {
			req.fieldTypes[cacheKey] = nil
		}
		return nil
	}
	// todo22 P2：共享请求级 AST 缓存（同一写入函数只读盘/解析一次）
	ctx := seqFileCtxFor(req, n.FilePath)
	if ctx == nil {
		if req.fieldTypes != nil {
			req.fieldTypes[cacheKey] = nil
		}
		return nil
	}
	f := ctx.file

	curPkg := pkgPathOf(writerID)
	imports := importAliases(f)
	// U06：只扫**写者函数自身**的函数体——整个文件里的同名 `.field` 写入
	// 属于别的对象/方法（`Mixed.Set` 的 `s.biz = v` 不能污染 `Known.biz`
	// 的来源证明）。
	scope := ast.Node(f)
	ast.Inspect(f, func(node ast.Node) bool {
		fd, ok := node.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			return true
		}
		start := ctx.fset.Position(fd.Pos()).Line
		end := ctx.fset.Position(fd.End()).Line
		if n.LineStart >= start && n.LineStart <= end {
			scope = fd.Body
			return false
		}
		return true
	})
	var out []writeSource
	ast.Inspect(scope, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) != 1 || len(n.Rhs) != 1 {
				return true
			}
			sel, ok := n.Lhs[0].(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != field {
				return true
			}
			// U06：写入来源无法证明（形参/局部变量/未知调用）→ 未知写者
			// 哨兵——不能因为别的写者具体就当唯一来源。
			srcs := rhsTypes(a, req, f, imports, n.Rhs[0], 3, curPkg)
			if len(srcs) == 0 {
				out = appendUnique(out, seqUnknownWriteSource())
				return true
			}
			out = appendUnique(out, srcs...)
			return true
		case *ast.CompositeLit:
			// todo24 §11.2：构造器里 `&Service{Biz: Bridge()}` 也是字段写入
			// （此前只认 `x.Biz = ...` 赋值，接口字段永远只能走接口候选）。
			for _, el := range n.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != field {
					continue
				}
				srcs := rhsTypes(a, req, f, imports, kv.Value, 3, curPkg)
				if len(srcs) == 0 {
					// U06：字段字面量值来源未证明（形参/未知调用）。
					out = appendUnique(out, seqUnknownWriteSource())
					continue
				}
				out = appendUnique(out, srcs...)
			}
		}
		return true
	})
	if req.fieldTypes != nil {
		req.fieldTypes[cacheKey] = out
	}
	return out
}
