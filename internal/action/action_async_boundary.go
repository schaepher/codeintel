package action

// todo14-6 / todo15-6：持久化/消息边界的「同一记录」静态证明。
//
// 只在证据齐备时升级，且必须来自实际值/实例/路径：
//   - 写入侧：实际 Send 实参的键（字段→同实例字面量 / 直接字面量 / 变量字面量）
//     与载荷实例一致；主题字面量来自该调用
//   - 读取侧：Receive 返回值绑定的键变量参与 ==/!= 比较，且字段读取位于
//     对应控制分支（== then / != else）
//   - 主题相同、键值相同、API 包身份一致 → 四跳 corroborated
//   - 键不同 → mismatch；!= 分支/无关变量/读取不在分支 → excluded/unverified
// 不拿别的字段常量补位，也不因方法短名/字符串相同独立升级。

import (
	"strings"
)

// AsyncBoundary 一条消息/存储边界的静态证据（写进 chain JSON）。
type AsyncBoundary struct {
	Kind            string `json:"kind,omitempty"` // queue|store
	Topic           string `json:"topic,omitempty"`
	Key             string `json:"key,omitempty"` // 实际发送键值
	KeyField        string `json:"key_field,omitempty"`
	ProducerAPI     string `json:"producer_api,omitempty"` // 实际调用目标（短形态）
	ConsumerAPI     string `json:"consumer_api,omitempty"`
	ProducerTarget  string `json:"producer_target,omitempty"` // 实际目标 canonical ID
	ConsumerTarget  string `json:"consumer_target,omitempty"`
	APIIdentity     string `json:"api_identity,omitempty"` // 实际包 + 类型（系统 API 身份）
	ProducerBinding string `json:"producer_binding,omitempty"`
	ConsumerBinding string `json:"consumer_binding,omitempty"`
	BindingSource   string `json:"binding_source,omitempty"`
	ProduceFile     string `json:"produce_file,omitempty"`
	ProduceLine     int    `json:"produce_line,omitempty"`
	SendKeyLine     int    `json:"send_key_line,omitempty"`
	ConsumeFile     string `json:"consume_file,omitempty"`
	ConsumeLine     int    `json:"consume_line,omitempty"`
	FilterLine      int    `json:"filter_line,omitempty"`
	ReadLine        int    `json:"read_line,omitempty"`
	FilterOp        string `json:"filter_op,omitempty"`
	Verdict         string `json:"verdict"` // corroborated|unverified|mismatch|ambiguous|excluded
	Detail          string `json:"detail,omitempty"`
}

// proveAsyncBoundary 写入/读取函数 + 字段 → 边界证明（无证据返回 nil）。
func (a *Actions) proveAsyncBoundary(producerID, consumerID, fieldPath string, readLine int,
	apiTargets map[apiCallKey]string) *AsyncBoundary {
	leaf := fieldLeaf(fieldPath)
	if leaf == "" {
		return nil
	}
	pf := a.parseFuncNode(producerID)
	cf := a.parseFuncNode(consumerID)
	if pf == nil || cf == nil {
		return nil
	}
	send := findSend(pf, leaf)
	recv := findRecv(cf, leaf, readLine)
	if send == nil && recv == nil {
		return nil
	}
	b := &AsyncBoundary{Kind: "queue", KeyField: leaf, ProduceFile: pf.file, ConsumeFile: cf.file}
	if send != nil {
		b.ProduceLine, b.SendKeyLine = send.line, send.keyLine
		b.Topic, b.Key, b.ProducerAPI = send.topic, send.keyValue, send.api
	}
	if recv != nil {
		b.ConsumeLine, b.ConsumerAPI = recv.line, recv.api
		if b.Topic == "" {
			b.Topic = recv.topic
		}
	}
	if send == nil {
		b.Verdict = "unverified"
		b.Detail = "写入侧未找到存储/消息发送调用（边界未建模）"
		return b
	}
	if send.issue != "" {
		b.Verdict = "ambiguous"
		if !strings.Contains(send.issue, "多个") {
			b.Verdict = "unverified"
		}
		b.Detail = send.issue
		return b
	}
	if recv == nil {
		b.Verdict = "unverified"
		b.Detail = "读取侧未找到主题 " + send.topic + " 的消费/查询调用（边界未闭环）"
		return b
	}
	if recv.issue != "" {
		b.Verdict = "unverified"
		b.Detail = recv.issue
		return b
	}
	if send.topic == "" || recv.topic == "" || send.topic != recv.topic {
		b.Verdict = "mismatch"
		b.Detail = "发送主题 " + send.topic + "（L" + itoaSeq(send.line) + "）与接收主题 " +
			recv.topic + "（L" + itoaSeq(recv.line) + "）不一致"
		return b
	}
	// todo17-6：实际 API 目标（调用边）——完整包/类型身份，不用调用方包名补位
	prodTarget := apiTargets[apiCallKey{funcID: producerID, line: send.line}]
	consTarget := apiTargets[apiCallKey{funcID: consumerID, line: recv.line}]
	if prodTarget == "" || consTarget == "" {
		b.Verdict = "unverified"
		b.ProducerAPI, b.ConsumerAPI = prodTarget, consTarget
		b.Detail = "未解析到实际 API 目标（调用边缺失），无法证明完整包/类型身份"
		return b
	}
	pPkg, pType, pMethod, pok := apiTargetIdentity(prodTarget)
	cPkg, cType, cMethod, cok := apiTargetIdentity(consTarget)
	if !pok || !cok {
		b.Verdict = "unverified"
		b.ProducerAPI, b.ConsumerAPI = prodTarget, consTarget
		b.Detail = "实际 API 目标不是方法形态（无法解析包/类型身份）"
		return b
	}
	b.ProducerAPI, b.ConsumerAPI = "("+pType+")."+pMethod, "("+cType+")."+cMethod
	b.ProducerTarget, b.ConsumerTarget = prodTarget, consTarget
	if pPkg != cPkg || pType != cType {
		b.Verdict = "unverified"
		b.APIIdentity = pPkg + "." + pType + " ↔ " + cPkg + "." + cType
		b.Detail = "生产/消费 API 目标包或接收者类型不同（" + b.APIIdentity + "），未证明同一消息/存储系统"
		return b
	}
	b.APIIdentity = pPkg + "." + pType
	// 系统/配置绑定：同类型只是候选，必须核对实例声明/配置
	sender := resolveBindingAt(pf, send.recvVar, send.pos)
	receiver := resolveBindingAt(cf, recv.recvVar, recv.pos)
	b.ProducerBinding, b.ConsumerBinding = bindingText(sender), bindingText(receiver)
	b.BindingSource = send.recvVar + "@" + pf.file + ":" + itoaSeq(sender.line) +
		" ↔ " + recv.recvVar + "@" + cf.file + ":" + itoaSeq(receiver.line)
	same, verdict, detail := sameBinding(sender, receiver)
	if !same {
		b.Verdict = verdict
		b.Detail = detail + "（" + b.BindingSource + "）"
		return b
	}
	b.Detail = "系统绑定：" + detail
	if b.Key == "" {
		b.Verdict = "unverified"
		b.Detail = "实际发送键不是可追踪字面量（" + send.keyExpr + "）"
		return b
	}
	if len(recv.filters) == 0 {
		b.Verdict = "unverified"
		b.Detail = "读取侧未发现 Receive 返回键参与的过滤谓词（值依赖缺失）"
		return b
	}
	// 只看键变量参与的比较；多个不同值 → 歧义
	vals := map[string]bool{}
	for _, f := range recv.filters {
		vals[f.value] = true
	}
	if len(vals) > 1 {
		b.Verdict = "ambiguous"
		b.FilterLine = recv.filters[0].line
		b.Detail = "读取侧对接收键有多个互斥过滤字面量，无法证明单次实际生命周期"
		return b
	}
	f := recv.filters[0]
	b.FilterLine = f.line
	b.FilterOp = f.op.String()
	b.ReadLine = readLine
	if !f.readOK {
		b.Verdict = "excluded"
		b.Detail = "过滤谓词 " + f.expr + " " + f.op.String() + " \"" + f.value +
			"\"（L" + itoaSeq(f.line) + "）；读取 L" + itoaSeq(readLine) +
			" 不在 key == \"" + b.Key + "\" 的控制分支内，排除该身份路径"
		return b
	}
	if f.value != b.Key {
		b.Verdict = "mismatch"
		b.Detail = "发送键实际为 \"" + b.Key + "\"（L" + itoaSeq(b.SendKeyLine) +
			"），过滤键为 \"" + f.value + "\"（L" + itoaSeq(f.line) +
			"）；两处值不一致，不构成同记录链"
		return b
	}
	b.Verdict = SeqVerdictCorroborated
	b.Detail = "系统绑定：" + detail + "；实际发送键 " + leaf + "=\"" + b.Key + "\"（L" + itoaSeq(b.SendKeyLine) +
		"）→ 主题 " + b.Topic + "（" + b.APIIdentity + "）→ 读取过滤 " +
		f.expr + " == \"" + f.value + "\"（L" + itoaSeq(f.line) + "）且读取 L" +
		itoaSeq(readLine) + " 位于相等分支"
	return b
}

// applyAsyncBoundaries 对每条身份链尝试边界证明；返回诊断。
func (a *Actions) applyAsyncBoundaries(chains []AsyncChain) []SeqDiagCount {
	var diags []SeqDiagCount
	apiTargets := a.apiCallTargetIndex()
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
		b := a.proveAsyncBoundary(wfn, rfn, c.Field, readLine, apiTargets)
		if b == nil {
			continue
		}
		c.Boundary = b
		if b.Verdict == SeqVerdictCorroborated {
			for j := range c.Hops {
				h := &c.Hops[j]
				switch h.Kind {
				case "write":
					h.Detail = "字段写入；实际记录键 " + b.KeyField + "=\"" + b.Key + "\"（L" + itoaSeq(b.SendKeyLine) + "）"
				case "persistence", "persistence_or_queue":
					h.Kind = "persistence_or_queue"
					h.Field = b.Topic
					h.File, h.Line = b.ProduceFile, b.ProduceLine
					h.Verdict = SeqVerdictCorroborated
					h.Detail = "发送实参键 \"" + b.Key + "\"（L" + itoaSeq(b.SendKeyLine) +
						"）；主题 " + b.Topic + "；API " + b.APIIdentity +
						"；绑定 " + b.BindingSource
				case "read":
					h.File, h.Line = b.ConsumeFile, b.ConsumeLine
					h.Verdict = SeqVerdictCorroborated
					h.Detail = "接收调用（主题 " + b.Topic + "）返回键；字段读取 L" + itoaSeq(readLine)
				case "filter":
					h.File, h.Line = b.ConsumeFile, b.FilterLine
					h.Verdict = SeqVerdictCorroborated
					h.Detail = "过滤键 == \"" + b.Key + "\"（L" + itoaSeq(b.FilterLine) + "）且读取位于相等分支"
				}
			}
			c.Verdict = SeqVerdictCorroborated
			c.Reason = "message_key"
			continue
		}
		// 未证明：把实际值/位置写进对应一跳，并明确排除/缺口
		for j := range c.Hops {
			h := &c.Hops[j]
			switch h.Kind {
			case "persistence", "persistence_or_queue":
				h.Kind = "persistence_or_queue"
				if b.ProduceFile != "" {
					h.File, h.Line = b.ProduceFile, b.ProduceLine
				}
				h.Field = b.Topic
				h.Detail = b.Detail
			case "filter":
				if b.FilterLine > 0 {
					h.File, h.Line = b.ConsumeFile, b.FilterLine
				}
				h.Detail = b.Detail
			case "read":
				if h.Detail == "" {
					h.Detail = "字段读取（SSA 摘要）"
				}
				h.Detail += "；读取 L" + itoaSeq(readLine) + "（不满足过滤分支/值一致）"
			}
		}
		code := "boundary_identity_unproven"
		switch b.Verdict {
		case "mismatch":
			code = "boundary_key_mismatch"
		case "ambiguous":
			code = "boundary_key_ambiguous"
		case "excluded":
			code = "boundary_condition_mismatch"
			c.Reason = "identity_excluded"
		}
		diags = append(diags, SeqDiagCount{Code: code, Count: 1,
			Detail: "字段 " + c.Field + "：" + b.Detail + "（生产跳 " + b.ProduceFile + ":" + itoaSeq(b.ProduceLine) +
				"；消费跳 " + b.ConsumeFile + ":" + itoaSeq(b.ConsumeLine) + "）"})
	}
	return diags
}
