package action

// U04k：时序展开的延迟绑定记录（depth=1 调用结果待续查后回填）。

import "go/ast"

// U04k：depth=1 时调用结果在续查完成后才知道（延迟绑定记录）。
type seqPendingVar struct {
	call *ast.CallExpr
	obj  *ast.Object
	slot int
	// env 登记时的函数环境（T12b：续查完成后把结果绑定回**该**环境，
	// 而不是收尾时根请求的环境）。
	env *seqEnv
}

type seqPendingCond struct {
	branch              *CodeSeqNode
	obj                 *ast.Object
	kind                string // write|rows
	whenTrue, whenFalse string
}

type seqPendingOrigin struct {
	obj   *ast.Object
	apply func(*SeqStorageOrigin)
}

// resultObjectsOf 具名结果的对象（无名结果返回 nil 占位）。
func resultObjectsOf(results *ast.FieldList) []*ast.Object {
	if results == nil {
		return nil
	}
	var out []*ast.Object
	for _, field := range results.List {
		if len(field.Names) == 0 {
			out = append(out, nil)
			continue
		}
		for _, n := range field.Names {
			out = append(out, n.Obj)
		}
	}
	return out
}
