package action

// todo22 §2：函数值环境——按 AST 对象身份保存绑定，支持分支合并与循环
// "可能未执行"的保守合并（多候选 → 不消歧）。

import "go/ast"

// seqTypeRef 接收者类型引用（todo22 §2.1：函数绑定保存 receiver 身份）。
type seqTypeRef struct {
	pkgPath  string
	typeName string
}

// seqEnv 一个函数调用的值环境（父环境 = 闭包捕获/外层绑定）。
// types 记录局部变量/形参的接收者类型（方法值 x.Method 用），同样按 AST
// 对象身份区分阴影。
type seqEnv struct {
	parent *seqEnv
	vals   map[*ast.Object]seqValue
	types  map[*ast.Object]seqTypeRef
}

func newSeqEnv(parent *seqEnv) *seqEnv {
	return &seqEnv{parent: parent, vals: map[*ast.Object]seqValue{}, types: map[*ast.Object]seqTypeRef{}}
}

func (e *seqEnv) lookup(obj *ast.Object) (seqValue, bool) {
	for env := e; env != nil; env = env.parent {
		if v, ok := env.vals[obj]; ok {
			return v, true
		}
	}
	return seqValue{}, false
}

func (e *seqEnv) set(obj *ast.Object, v seqValue) {
	if e == nil || obj == nil {
		return
	}
	e.vals[obj] = v
}

// clone 复制本层（父环境共享——闭包捕获的是外层绑定，不是快照）。
func (e *seqEnv) clone() *seqEnv {
	if e == nil {
		return nil
	}
	out := &seqEnv{parent: e.parent,
		vals:  make(map[*ast.Object]seqValue, len(e.vals)),
		types: make(map[*ast.Object]seqTypeRef, len(e.types))}
	for k, v := range e.vals {
		out.vals[k] = v
	}
	for k, v := range e.types {
		out.types[k] = v
	}
	return out
}

// mergeBranches 分支合并（就地更新本层）：被任一分支改写的对象取各分支
// 候选值；全部相同 → 单值，否则 multi（保留未消歧）。调用方传入的
// branches 是各分支的 clone。
func (e *seqEnv) mergeBranches(branches []*seqEnv) {
	if e == nil || len(branches) == 0 {
		return
	}
	seen := map[*ast.Object]bool{}
	var objs []*ast.Object
	for _, br := range branches {
		if br == nil {
			continue
		}
		for obj := range br.vals {
			if !seen[obj] {
				seen[obj] = true
				objs = append(objs, obj)
			}
		}
	}
	for _, obj := range objs {
		var cands []seqValue
		for _, br := range branches {
			if br == nil {
				continue
			}
			if v, ok := br.vals[obj]; ok {
				cands = append(cands, v)
			}
		}
		if len(cands) == 0 {
			continue
		}
		e.set(obj, multiSeqValue(cands, "分支/循环下多个值都可能到达调用点（未消歧）"))
	}
}

// sameSeqValue 两个函数值是否同一来源（kind + 字面量/符号/未知原因）。
func sameSeqValue(a, b seqValue) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case seqValueLit:
		return a.lit == b.lit
	case seqValueNamed:
		return a.symID == b.symID
	case seqValueUnknown:
		return a.why == b.why
	case seqValueList:
		if len(a.elems) != len(b.elems) {
			return false
		}
		for i := range a.elems {
			if !sameSeqValue(a.elems[i], b.elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// mergePossible 循环体可能 0 次执行：当前值与原值都作为候选。
func (e *seqEnv) mergePossible(pre *seqEnv) {
	if e == nil || pre == nil {
		return
	}
	seen := map[*ast.Object]bool{}
	var objs []*ast.Object
	for _, env := range []*seqEnv{e, pre} {
		for obj := range env.vals {
			if !seen[obj] {
				seen[obj] = true
				objs = append(objs, obj)
			}
		}
	}
	for _, obj := range objs {
		var cands []seqValue
		cur, hasCur := e.vals[obj]
		old, hasOld := pre.vals[obj]
		if hasCur {
			cands = append(cands, cur)
		}
		if hasOld {
			cands = append(cands, old)
		}
		// todo24 §11.5：循环体**没有改写**该槽（只读回调）时保持原来源——
		// 不能仅因为"循环可能不执行"就把确定值降级成多候选。
		if hasCur && hasOld && sameSeqValue(cur, old) {
			continue
		}
		e.set(obj, multiSeqValue(cands, "循环体可能未执行（值可能仍是循环前的）"))
	}
}
