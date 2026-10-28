package action

// todo23 §2.5：防环键与回调上下文（从 action_seq_funcvalue_exec.go 拆出）。

// seqVisitKey 防环键：repo + 符号 + 入站回调上下文（todo23 §2.5）。
// 不能只用符号 ID（同一函数经不同回调路径应可继续展开），也不能用剩余
// 深度/每层新对象地址（会制造假身份、失去防环）。
func seqVisitKey(req CodeSequenceRequest, symID string) string {
	return req.RepoAbs + "|" + symID + "|" + req.callbackCtx
}

// seqCallbackVisitCtx 函数值的词法身份 + 绑定站点（回调上下文）。
func seqCallbackVisitCtx(fv seqValue) string {
	base := ""
	switch fv.kind {
	case seqValueLit:
		base = "lit:" + seqLitKey(fv)
	case seqValueNamed:
		base = "fn:" + fv.symID
	default:
		return ""
	}
	if fv.bindFile != "" {
		base += "@" + fv.bindFile + ":" + itoaSeq(fv.bindLine)
	}
	if fv.declObj != nil && fv.declObj.Name != "" {
		base += "#" + fv.declObj.Name
	}
	return base
}
