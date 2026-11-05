package action

// U04f：实例条件化判定（从 action_seq_workflow_instance.go 拆出）。

// seqWFEvalOperand 条件操作数的条件化判定值（literal/nil）。
func seqWFEvalOperand(o *SeqStorageOrigin, readVals map[string]map[string]*SeqStorageOrigin,
	guards map[string]bool, premises *[]string) (string, string, bool) {
	if o == nil {
		return "", "", false
	}
	switch o.Kind {
	case SeqOriginNil:
		return "nil", "", true
	case SeqOriginLiteral:
		return "literal", o.Literal, true
	case SeqOriginReadColumn:
		rc := o.ReadColumn
		if rc == nil || rc.Result == nil || rc.Result.Read == nil {
			return "", "", false
		}
		scanID := rc.Result.ID
		if !guards[scanID] {
			*premises = appendUniqueStr(*premises, SeqProofScanGuard)
			return "", "", false
		}
		vals := readVals[rc.Result.Read.ID]
		if vals == nil || vals[rc.Column] == nil {
			return "", "", false
		}
		v := vals[rc.Column]
		if v.Kind != SeqOriginLiteral {
			return "", "", false
		}
		*premises = appendUniqueStr(*premises, SeqProofInstanceSubset)
		return "literal", v.Literal, true
	}
	return "", "", false
}

// seqWFEvalCondition 条件化判定（true/false/unknown + 前提）。
func seqWFEvalCondition(c *SeqCondition, readVals map[string]map[string]*SeqStorageOrigin,
	guards map[string]bool, premises *[]string) (string, *SeqStorageOrigin, *SeqStorageOrigin) {
	if c == nil {
		return "unknown", nil, nil
	}
	switch c.Operator {
	case SeqConditionOpAnd, SeqConditionOpOr:
		anyUnknown := false
		allTrue, allFalse := true, true
		for i := range c.Terms {
			t, _, _ := seqWFEvalCondition(&c.Terms[i], readVals, guards, premises)
			switch t {
			case "true":
				allFalse = false
			case "false":
				allTrue = false
			default:
				anyUnknown = true
			}
		}
		if c.Operator == SeqConditionOpAnd {
			if allTrue && !anyUnknown {
				return "true", c.Left, c.Right
			}
			for i := range c.Terms {
				t, _, _ := seqWFEvalCondition(&c.Terms[i], readVals, guards, premises)
				if t == "false" {
					return "false", c.Left, c.Right
				}
			}
			return "unknown", c.Left, c.Right
		}
		if allFalse && !anyUnknown {
			return "false", c.Left, c.Right
		}
		for i := range c.Terms {
			t, _, _ := seqWFEvalCondition(&c.Terms[i], readVals, guards, premises)
			if t == "true" {
				return "true", c.Left, c.Right
			}
		}
		return "unknown", c.Left, c.Right
	case SeqConditionOpNot:
		if len(c.Terms) == 0 {
			return "unknown", c.Left, c.Right
		}
		t, _, _ := seqWFEvalCondition(&c.Terms[0], readVals, guards, premises)
		switch t {
		case "true":
			return "false", c.Left, c.Right
		case "false":
			return "true", c.Left, c.Right
		}
		return "unknown", c.Left, c.Right
	case SeqConditionOpBool:
		if c.Left != nil && c.Left.Kind == SeqOriginLiteral && c.Left.ScalarType == "bool" {
			if c.Left.Literal == "true" {
				return "true", c.Left, c.Right
			}
			if c.Left.Literal == "false" {
				return "false", c.Left, c.Right
			}
		}
		return "unknown", c.Left, c.Right
	}
	lk, lv, lok := seqWFEvalOperand(c.Left, readVals, guards, premises)
	rk, rv, rok := seqWFEvalOperand(c.Right, readVals, guards, premises)
	if !lok || !rok {
		return "unknown", c.Left, c.Right
	}
	if lk != rk {
		return "unknown", c.Left, c.Right
	}
	cmp := seqWFCmpLiteral(c.Operator, lk, lv, rv)
	return cmp, c.Left, c.Right
}

// seqWFCmpLiteral 字面量比较（未知运算符/类型 → unknown；nil 只支持 ==/!=）。
func seqWFCmpLiteral(op, kind, a, b string) string {
	switch op {
	case "==":
		if a == b {
			return "true"
		}
		return "false"
	case "!=":
		if a != b {
			return "true"
		}
		return "false"
	case "<", "<=", ">", ">=":
		if kind != "literal" {
			return "unknown"
		}
		x, xok := parseWFNum(a)
		y, yok := parseWFNum(b)
		if !xok || !yok {
			return "unknown"
		}
		switch op {
		case "<":
			if x < y {
				return "true"
			}
			return "false"
		case "<=":
			if x <= y {
				return "true"
			}
			return "false"
		case ">":
			if x > y {
				return "true"
			}
			return "false"
		default:
			if x >= y {
				return "true"
			}
			return "false"
		}
	}
	return "unknown"
}
