package action

// U04b：键值匹配与连接构建（从 action_seq_workflow_conn.go 拆出）。

// seqWFValueLiteral 可折叠字面量（字符串/布尔/数值）；未知返回空。
func seqWFValueLiteral(v SeqStorageValue) (string, bool) {
	if v.Kind == SeqStorageValueLiteral {
		return v.SQL, true
	}
	if v.Origin != nil && v.Origin.Kind == SeqOriginLiteral {
		return v.Origin.Literal, true
	}
	return "", false
}

// seqWFCompatible 两个键值是否可条件化匹配（固定值必须相等；变量-变量可匹配）。
func seqWFCompatible(a, b SeqStorageValue) bool {
	la, aok := seqWFValueLiteral(a)
	lb, bok := seqWFValueLiteral(b)
	if aok || bok {
		return aok && bok && la == lb
	}
	return true
}

// seqWFWriteColumn 写入列值（write values 优先，其次 WHERE 等式）。
func seqWFWriteColumn(w seqWFWrite, column string) (SeqStorageValue, bool) {
	for _, v := range w.Values {
		if v.Column == column {
			return v.Value, true
		}
	}
	for _, p := range w.Preds {
		if p.Column == column {
			return p.Value, true
		}
	}
	return SeqStorageValue{}, false
}
