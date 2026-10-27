package action

// todo22 §4.1：RPC 续接的停止码与证据支持程度（从 action_seq_evidence.go
// 拆出，保持该文件在 300 行门禁内；语义与其它展开来源一致）。

import "strings"

// RPC 服务端续接的明确未命中/歧义（正常未命中要缓存，不当作查询故障；
// 实现方法缺失与"没找到注册"分开）。
const (
	SeqStopRPCRegistrationMissing = "rpc_registration_missing"
	SeqStopRPCImplMissing         = "rpc_impl_missing"
	SeqStopRPCMethodMissing       = "rpc_method_missing"
	SeqStopRPCAmbiguous           = "rpc_ambiguous"
	SeqStopRPCQueryFailed         = "rpc_query_failed"
	// 客户端声明的服务身份与服务端注册身份不一致（改名/配错）——fail-closed，
	// 不按短名兜底续接
	SeqStopRPCIdentityConflict = "rpc_identity_conflict"
)

// seqRPCEvidenceVerdict RPC 续接证据的支持程度：客户端调用点签名已证明协议
// （go-micro），完整身份精确匹配 → corroborated；只按短名/端点匹配
// （身份不完整）→ unverified（跨服务串接风险，不得当确定结论）。
func seqRPCEvidenceVerdict(ev *SeqEvidence) string {
	if ev.SymbolID == "" {
		return SeqVerdictUnverified
	}
	if strings.HasPrefix(ev.Reason, "micro-register-identity") ||
		strings.HasPrefix(ev.Reason, "micro-register-handler-name") && ev.RPCService != "" {
		return SeqVerdictCorroborated
	}
	if strings.HasPrefix(ev.Reason, "micro-register-handler-name") {
		return SeqVerdictUnverified
	}
	return SeqVerdictCorroborated
}
