package action

// T12：兼容续查入口 Continue{MaxSegments} —— 与原轮次模式互斥、范围 1–128、
// 同语义（同步续接、按总额外展开次数计数），元数据保留两种模式预算。

import (
	"strings"
	"testing"
)

func TestSeqContinuationCompatNormalization(t *testing.T) {
	// 合法：Compat 归一到 ContinueSegments 同一引擎。
	req := CodeSequenceRequest{Continue: &SeqContinuationOptions{MaxSegments: 3}}
	if err := req.normalizeContinuation(); err != nil {
		t.Fatalf("合法 Compat 被拒: %v", err)
	}
	if req.ContinueSegments != 3 || !req.continuationCompat {
		t.Fatalf("Compat 未归一: %+v", req)
	}
	// 互斥：两种入口同时给出 → 拒绝。
	both := CodeSequenceRequest{ContinueSegments: 2, Continue: &SeqContinuationOptions{MaxSegments: 2}}
	if err := both.normalizeContinuation(); err == nil || !strings.Contains(err.Error(), "互斥") {
		t.Fatalf("两种入口必须互斥: %v", err)
	}
	// 范围：0 / 129 拒绝。
	for _, n := range []int{0, 129, -1} {
		r := CodeSequenceRequest{Continue: &SeqContinuationOptions{MaxSegments: n}}
		if err := r.normalizeContinuation(); err == nil {
			t.Errorf("MaxSegments=%d 应被拒绝", n)
		}
	}
	// 原入口范围同样校验（>128 拒绝）。
	r := CodeSequenceRequest{ContinueSegments: SeqContinuationMaxSegments + 1}
	if err := r.normalizeContinuation(); err == nil {
		t.Error("ContinueSegments 超上限应被拒绝")
	}
}

// 兼容模式元数据：预算/模式/总展开次数；前沿记录与轮次模式一致。
func TestSeqContinuationCompatMeta(t *testing.T) {
	req := CodeSequenceRequest{Continue: &SeqContinuationOptions{MaxSegments: 2}}
	if err := req.normalizeContinuation(); err != nil {
		t.Fatal(err)
	}
	req.acc = &seqTimingAcc{}
	node := depthStopNode("symbol:go:example.com/m:First")
	seqRegisterFrontier(req, node, "symbol:go:example.com/m:First", func(CodeSequenceRequest) {}, node.Stop.Detail)
	seqContinueSegments(New(nil), req, &CodeSeqNode{})
	meta := seqContinuationMeta(req)
	if meta == nil {
		t.Fatal("兼容模式应有 continuation 元数据")
	}
	if !meta.CompatMode || meta.CompatMaxSegment != 2 {
		t.Errorf("兼容模式元数据 = %+v", meta)
	}
	if meta.ExtraExpansions != 1 {
		t.Errorf("总展开次数 = %d, want 1", meta.ExtraExpansions)
	}
	if len(meta.Frontiers) != 1 || !meta.Frontiers[0].Continued {
		t.Errorf("前沿记录 = %+v", meta.Frontiers)
	}
}

// 身份变化：续查时目标身份与登记不一致 → 保留原前沿并标 identity_changed。
func TestSeqContinuationIdentityChanged(t *testing.T) {
	req := continueReq(1)
	req.acc = &seqTimingAcc{}
	node := depthStopNode("symbol:go:example.com/m:First")
	seqRegisterFrontier(req, node, "symbol:go:example.com/m:First", func(CodeSequenceRequest) {
		t.Error("身份变化时不应按新身份展开")
	}, node.Stop.Detail)
	// 续查前同一调用点解析到另一个符号
	node.Evidence.SymbolID = "symbol:go:example.com/m:Other"
	seqContinueSegments(New(nil), req, &CodeSeqNode{})
	meta := seqContinuationMeta(req)
	if meta == nil || len(meta.Frontiers) != 1 {
		t.Fatalf("meta = %+v", meta)
	}
	f := meta.Frontiers[0]
	if !f.IdentityChanged || f.Continued {
		t.Errorf("应保留原前沿并标 identity_changed: %+v", f)
	}
	if f.TargetID != "symbol:go:example.com/m:First" {
		t.Errorf("原前沿身份应保留: %+v", f)
	}
	if !strings.Contains(f.IdentityReason, "identity_changed") {
		t.Errorf("identity_reason = %q", f.IdentityReason)
	}
}

// 请求身份：兼容入口与轮次入口即使段数相同也不复用同一结论。
func TestSeqContinuationCompatConfigIdentity(t *testing.T) {
	round := CodeSequenceRequest{Depth: 2, ContinueSegments: 2}
	compat := CodeSequenceRequest{Depth: 2, Continue: &SeqContinuationOptions{MaxSegments: 2}}
	if err := compat.normalizeContinuation(); err != nil {
		t.Fatal(err)
	}
	if seqConfigHash(round) == seqConfigHash(compat) {
		t.Error("兼容入口应进入请求身份（config_hash 不同）")
	}
}
