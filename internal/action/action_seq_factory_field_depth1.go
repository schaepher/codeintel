package action

import (
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqExpandFieldImpls 字段来源实现候选 → 展开/歧义/未知（单实现也要求具体
// 类型；接口方法 ID 不是实例来源）。返回 true 表示已处理节点。
func seqExpandFieldImpls(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	impls []string, depth int, curFile string, pos token.Position) bool {
	concrete := make([]string, 0, len(impls))
	for _, impl := range impls {
		if a.isIfaceMethodID(impl) {
			continue
		}
		concrete = appendUniqueStr(concrete, impl)
	}
	if len(concrete) == 0 {
		markSeqUnknownReceiver(node, curFile, pos.Line, 0.8, "字段", "实例来源未知")
		return true
	}
	if len(concrete) > 1 {
		node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
			CallLine: pos.Line, Candidates: concrete, Ambiguous: true,
			Reason: "receiver-field-dataflow", CandidateBudget: seqIfaceBudget(len(concrete), false)}
		node.Stop = &SeqStop{Code: SeqStopIfaceAmbiguous,
			Detail: "字段实现来源有 " + itoaSeq(len(concrete)) + " 个候选（未消歧，未展开）"}
		capped, truncated := capSeqCandidates(concrete)
		seqExpandCandidateBranches(a, req, node, capped, depth, pos, truncated, len(concrete))
		return true
	}
	impl := concrete[0]
	node.ImplType = implTypeShort(impl)
	node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
		CallLine: pos.Line, SymbolID: impl, Confidence: 0.8,
		Reason: "receiver-field-dataflow"}
	seqExpandInto(a, req, node, impl, depth)
	return true
}

// seqFactoryFieldImplsAtDepth1 工厂来源 receiver 的 getter 字段在 depth=1 的
// 身份解析（与完整展开同一口径；不展开子树）。返回具体实现方法候选 + 字段名。
func (a *Actions) seqFactoryFieldImplsAtDepth1(req CodeSequenceRequest, recvType, method string) ([]string, string) {
	if req.curRecvOrigin == "" || req.curCtx == nil || req.curCtx.file == nil {
		return nil, ""
	}

	owners := seqMethodOwnerTypes(a, req, req.curFile, recvType, "Get")
	if len(owners) != 1 {
		return nil, ""
	}
	getID := "symbol:go:" + pkgOfID(domain.CanonicalID(req.curSym)) + ":(" + owners[0] + ").Get"
	field, ok := seqMethodReturnField(a, req, getID, owners[0])
	if !ok || field == "" {
		return nil, ""
	}
	cands, ok := seqFactoryFieldCandidates(a, req, req.curRecvOrigin, recvType, field,
		pkgPathOf(req.curSym))
	if !ok || len(cands) == 0 {
		return nil, field
	}
	var impls []string
	for _, c := range cands {
		if impl := GrpcMethodEntryID("symbol:go:"+c.pkgPath+":"+c.typeName, method); impl != "" {
			impls = appendUniqueStr(impls, impl)
		}
	}
	return impls, field
}
