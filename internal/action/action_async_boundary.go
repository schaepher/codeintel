package action

// todo14-6：持久化/消息边界的「同一记录」静态证明。
//
// 判据（只在证据齐备时升级，表列映射/同名指纹不算）：
//   - 写入侧：记录键字段被赋值为**字面量**（记录键值 K），并调用
//     存储/消息 API（Send/Publish/Consume/Put/Save/Get…），实参含主题/表
//     字面量 T 与键（字段或字面量 K）
//   - 读取侧：调用同一 API 族且主题字面量同为 T，并用字面量 K 做过滤
//     （== K / 调用实参 K）
//   - 两条路径各自有真实源码位置；K 唯一（多分支字面量 → 歧义，不升级）
// 证明成功：chain.Verdict=corroborated、identity_partial=false，逐跳带
// 文件/行/键；失败：保留 unverified 并给出具体不一致的一跳与位置。

import (
	"strings"
)

// AsyncBoundary 一条消息/存储边界的静态证据（写进 chain JSON）。
type AsyncBoundary struct {
	Kind        string `json:"kind,omitempty"` // queue|store
	Topic       string `json:"topic,omitempty"`
	Key         string `json:"key,omitempty"`
	KeyField    string `json:"key_field,omitempty"`
	ProduceFile string `json:"produce_file,omitempty"`
	ProduceLine int    `json:"produce_line,omitempty"`
	ConsumeFile string `json:"consume_file,omitempty"`
	ConsumeLine int    `json:"consume_line,omitempty"`
	FilterLine  int    `json:"filter_line,omitempty"`
	Verdict     string `json:"verdict"` // corroborated|unverified|mismatch|ambiguous
	Detail      string `json:"detail,omitempty"`
}

var (
	asyncProducerCalls = map[string]bool{"send": true, "sendmessage": true, "publish": true,
		"produce": true, "put": true, "save": true, "insert": true, "write": true, "store": true}
	asyncConsumerCalls = map[string]bool{"receive": true, "consumemessage": true, "consume": true,
		"subscribe": true, "read": true, "get": true, "find": true, "findbyid": true,
		"load": true, "fetch": true, "query": true}
)

// readFileCached 读取源码文件（边界分析一次查询内复用；失败返回错误）。

// fieldLiteralAssignments 记录键字段被赋的字面量（字段赋值 + 复合字面量）。

// boundaryCall 一条候选边界调用（producer 或 consumer）。

// consumerFilterLiterals 读取侧的过滤字面量（== / != 比较 + 调用实参）。

// proveAsyncBoundary 写入/读取函数 + 字段 → 边界证明（无证据返回 nil）。
func (a *Actions) proveAsyncBoundary(producerID, consumerID, fieldPath string, readLine int) *AsyncBoundary {
	leaf := fieldLeaf(fieldPath)
	if leaf == "" {
		return nil
	}
	pf := a.parseFuncNode(producerID)
	cf := a.parseFuncNode(consumerID)
	if pf == nil || cf == nil {
		return nil
	}
	prodCalls := collectBoundaryCalls(pf, asyncProducerCalls, leaf)
	consCalls := collectBoundaryCalls(cf, asyncConsumerCalls, leaf)
	if len(prodCalls) == 0 && len(consCalls) == 0 {
		return nil
	}
	literals := fieldLiteralAssignments(pf, leaf)
	// 多分支不同键值 → 歧义（不把互斥路径串成一次生命周期）
	uniq := map[string]bool{}
	for _, l := range literals {
		uniq[l.value] = true
	}
	if len(uniq) > 1 {
		var vals []string
		for v := range uniq {
			vals = append(vals, v)
		}
		sortStrings(vals)
		b := &AsyncBoundary{Kind: "ambiguous", KeyField: leaf, Verdict: "ambiguous",
			Detail:      "记录键有多个互斥字面量（" + strings.Join(vals, ", ") + "），无法证明一次实际生命周期",
			ProduceFile: pf.file}
		if len(prodCalls) > 0 {
			b.ProduceLine = prodCalls[0].line
		}
		return b
	}
	var prod, cons *boundaryCall
	for i := range prodCalls {
		if prodCalls[i].topic != "" {
			prod = &prodCalls[i]
			break
		}
	}
	for i := range consCalls {
		if prod != nil && consCalls[i].topic == prod.topic {
			cons = &consCalls[i]
			break
		}
	}
	if prod == nil {
		if len(prodCalls) > 0 {
			return &AsyncBoundary{Kind: "store", KeyField: leaf, Verdict: "unverified",
				ProduceFile: pf.file, ProduceLine: prodCalls[0].line,
				Detail: "写入侧边界调用未提供可比较的主题/表字面量——边界未建模"}
		}
		return nil
	}
	b := &AsyncBoundary{Kind: "queue", Topic: prod.topic, KeyField: leaf,
		ProduceFile: pf.file, ProduceLine: prod.line, ConsumeFile: cf.file,
		Key: func() string {
			if len(literals) == 1 {
				return literals[0].value
			}
			return prod.key
		}(),
	}
	if cons != nil {
		b.ConsumeLine = cons.line
	}
	if b.Key == "" {
		b.Verdict = "unverified"
		b.Detail = "记录键不是字面量（" + strings.TrimSpace(prod.keyExpr) + "），无法证明同一记录"
		return b
	}
	if cons == nil {
		b.Verdict = "unverified"
		b.Detail = "读取侧未找到主题 " + prod.topic + " 的消费/查询调用（边界未闭环）"
		return b
	}
	filters := consumerFilterLiterals(cf)
	matchLine := 0
	for _, f := range filters {
		if f.value == b.Key {
			matchLine = f.line
			break
		}
	}
	if matchLine == 0 {
		b.Verdict = "mismatch"
		b.Detail = "读取过滤键与写入键不一致（期望 " + b.Key + "）"
		for _, f := range filters {
			if f.value != prod.topic {
				b.Detail += "；实际过滤 " + f.value + "（L" + itoaSeq(f.line) + "）"
				break
			}
		}
		return b
	}
	b.FilterLine = matchLine
	if readLine > 0 {
		b.ConsumeLine = cons.line
	}
	b.Verdict = SeqVerdictCorroborated
	b.Detail = "写入键 " + leaf + "=" + b.Key + " → 消息主题 " + b.Topic +
		"/键 " + b.Key + " → 读取过滤键一致"
	return b
}

// applyAsyncBoundaries 对每条身份链尝试边界证明；返回诊断（不一致/歧义/
// 未建模）。证明成功时重写逐跳为「写入键 → 消息/存储键 → 读取/过滤键」。
func (a *Actions) applyAsyncBoundaries(chains []AsyncChain) []SeqDiagCount {
	var diags []SeqDiagCount
	for i := range chains {
		c := &chains[i]
		wfn, rfn, readLine := "", "", 0
		for _, h := range c.Hops {
			switch h.Kind {
			case "write":
				wfn = h.Function
			case "read":
				rfn, readLine = h.Function, h.Line
			}
		}
		if wfn == "" || rfn == "" {
			continue
		}
		b := a.proveAsyncBoundary(wfn, rfn, c.Field, readLine)
		if b == nil {
			continue
		}
		c.Boundary = b
		if b.Verdict == SeqVerdictCorroborated {
			for j := range c.Hops {
				h := &c.Hops[j]
				switch h.Kind {
				case "write":
					h.Detail = "字段写入；记录键 " + b.KeyField + "=\"" + b.Key + "\"（源码赋值）"
				case "persistence", "persistence_or_queue":
					h.Kind = "persistence_or_queue"
					h.Field = b.Topic
					h.File, h.Line = b.ProduceFile, b.ProduceLine
					h.Verdict = SeqVerdictCorroborated
					h.Detail = "消息/存储边界：主题 " + b.Topic + "；记录键 " + b.Key + "（Send/Write 调用）"
				case "read":
					h.File, h.Line = b.ConsumeFile, b.ConsumeLine
					h.Verdict = SeqVerdictCorroborated
					h.Detail = "消费/查询调用（同一主题 " + b.Topic + "）"
				case "filter":
					h.File, h.Line = b.ConsumeFile, b.FilterLine
					h.Verdict = SeqVerdictCorroborated
					h.Detail = "过滤键 == \"" + b.Key + "\"（与写入键一致）"
				}
			}
			c.Verdict = SeqVerdictCorroborated
			c.Reason = "message_key"
			continue
		}
		// 未证明：把具体一跳/位置写进 hop + 诊断（不升级）
		for j := range c.Hops {
			h := &c.Hops[j]
			if h.Kind == "persistence" || h.Kind == "persistence_or_queue" {
				h.Kind = "persistence_or_queue"
				if b.ProduceFile != "" {
					h.File, h.Line = b.ProduceFile, b.ProduceLine
				}
				h.Field = b.Topic
				h.Detail = b.Detail
			}
			if h.Kind == "filter" && b.ConsumeLine > 0 {
				h.File, h.Line = b.ConsumeFile, b.FilterLine
				h.Detail = b.Detail
			}
		}
		code := "boundary_identity_unproven"
		switch b.Verdict {
		case "mismatch":
			code = "boundary_key_mismatch"
		case "ambiguous":
			code = "boundary_key_ambiguous"
		}
		diags = append(diags, SeqDiagCount{Code: code, Count: 1,
			Detail: "字段 " + c.Field + "：" + b.Detail + "（生产跳 " + b.ProduceFile + ":" + itoaSeq(b.ProduceLine) +
				"；消费跳 " + b.ConsumeFile + ":" + itoaSeq(b.ConsumeLine) + "）"})
	}
	return diags
}
