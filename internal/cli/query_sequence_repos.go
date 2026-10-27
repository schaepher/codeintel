package cli

// todo22 §4.1：`query sequence --code` 的跨仓续接候选（与 ext-chain 同源，
// 默认更保守）。从 query_sequence_code.go 拆出（行数治理）。

import "strings"

// seqCandidateRepos RPC 跨仓续接候选（todo22 §4.1）——与 ext-chain 同源但
// **默认更保守**：起始仓库不在 workspace 时只查本仓（时序分析的默认结果
// 不应因全局注册表里存在同名服务而改变）；--all-repos 显式扩展为全部已注册。
func seqCandidateRepos(self string, allRepos bool) (out []string, scope, reason string) {
	all, allScope, allReason := extChainCandidateRepos(self, allRepos)
	if allRepos {
		return all, allScope, allReason
	}
	if strings.HasPrefix(allScope, "workspace:") {
		return all, allScope, allReason
	}
	return nil, "repo-only", "起始仓库无 workspace 归属：仅本仓（--all-repos 显式扩展）"
}
