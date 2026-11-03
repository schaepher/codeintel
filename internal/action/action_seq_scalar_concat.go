package action

// U04c：字符串拼接来源（有界 concat/parts 树）。

import "go/ast"

// seqConcatOrigin 字符串拼接来源（两侧都必须已证明为 string 来源）。
func seqConcatOrigin(left, right *SeqStorageOrigin) *SeqStorageOrigin {
	if !seqOriginIsString(left) || !seqOriginIsString(right) {
		return nil
	}
	parts := make([]SeqStorageOrigin, 0, 2)
	appendParts := func(o *SeqStorageOrigin) {
		if o.Kind == SeqOriginConcat {
			parts = append(parts, o.Parts...)
			return
		}
		parts = append(parts, *cloneSeqScalarOrigin(o))
	}
	appendParts(left)
	appendParts(right)
	if len(parts) > 32 || seqOriginTotalBytes(parts) > seqScalarOriginMaxBytes {
		return nil
	}
	return &SeqStorageOrigin{Kind: SeqOriginConcat, ScalarType: "string", Parts: parts}
}

func seqOriginIsString(o *SeqStorageOrigin) bool {
	if o == nil || o.ScalarType != "string" {
		return false
	}
	// 只有 parameter/literal(string)/concat 的字符串来源可继续拼接。
	switch o.Kind {
	case SeqOriginParameter, SeqOriginConcat:
		return true
	case SeqOriginLiteral:
		return true
	}
	return false
}

func seqOriginTotalBytes(parts []SeqStorageOrigin) int {
	total := 0
	for i := range parts {
		total += len(parts[i].Literal)
		for j := range parts[i].Parts {
			total += len(parts[i].Parts[j].Literal)
		}
	}
	return total
}

// captureSeqArgOrigins 调用点每个实参的标量来源（在实参求值位置立即
// 捕获：后续赋值/改写不影响已捕获的来源）。
func captureSeqArgOrigins(req CodeSequenceRequest, call *ast.CallExpr) []*SeqStorageOrigin {
	if call == nil {
		return nil
	}
	out := make([]*SeqStorageOrigin, len(call.Args))
	for i, arg := range call.Args {
		if call.Ellipsis.IsValid() && i == len(call.Args)-1 {
			// `f(xs...)` 的实参是集合，标量来源未证明。
			out[i] = nil
			continue
		}
		out[i] = seqScalarOriginOf(req, arg)
	}
	return out
}
