package action

// U06：接口字段/接收者实例来源未知的边界判定（从 action_seq_walk_call.go
// 拆出——行数治理）：唯一实现不等于实际实例来源。

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// markSeqUnknownReceiver 把节点标成"实例来源未知"：保留接口调用与未验证
// 边界（Ambiguous 让渲染加 "? " 前缀），不展开任何实现。
func markSeqUnknownReceiver(node *CodeSeqNode, curFile string, line int,
	confidence float64, recvType, field string) {
	node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
		CallLine: line, Confidence: confidence,
		Reason: "unknown_receiver", Ambiguous: true}
	node.Stop = &SeqStop{Code: SeqStopUnknownReceiver,
		Detail: "字段 " + recvType + "." + field + " 无写入/注入证明（唯一实现不等于实际实例来源，未展开）"}
}

// seqDeclaredTypeIsIface 字段/变量声明类型是否为已索引的接口（同包）。
func seqDeclaredTypeIsIface(a *Actions, req CodeSequenceRequest, declType string) bool {
	name := strings.TrimPrefix(strings.TrimSpace(declType), "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || a == nil || a.repo == nil {
		return false
	}
	n, err := a.repo.GetSymbol(domain.CanonicalID("symbol:go:" + pkgOfID(domain.CanonicalID(req.curSym)) + ":" + name))
	if err != nil || n == nil {
		return false
	}
	return n.Kind == domain.KindInterface
}
