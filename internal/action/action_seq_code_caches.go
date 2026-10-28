package action

import "github.com/schaepher/codeintel/internal/domain"

// todo22 P1/P2：CodeSequence 请求级缓存的初始化（从 action_seq_code.go 拆出——
// 行数治理）。全部按请求隔离；跨仓 RPC 子树会显式清空。

func seqInitRequestCaches(req *CodeSequenceRequest) {
	if req == nil {
		return
	}
	if req.fileCache == nil {
		req.fileCache = map[string]*seqParsedFile{}
	}
	if req.litVisited == nil {
		req.litVisited = map[string]bool{} // todo22 §2：函数字面量展开防环
	}
	if req.tgtsCache == nil {
		req.tgtsCache = map[string]*seqTargets{} // todo22 §2：按函数缓存调用点映射
	}
	if req.ifaceImpls == nil {
		req.ifaceImpls = map[string]seqIfaceCached{} // todo22 §7.3：接口候选请求内缓存
	}
	if req.ifaceImplTypes == nil {
		req.ifaceImplTypes = map[string][]string{} // todo23 §7.1：接口实现类型
	}
	if req.extPkgs == nil {
		req.extPkgs = map[string]*seqExternalPkg{} // todo22 §4.2：外部包源码缓存
	}
	if req.constPkgs == nil {
		req.constPkgs = map[string]map[string]string{} // todo22 P1：跨包常量表
	}
	if req.diRegs == nil {
		req.diRegs = map[string][]seqDIReg{}
	}
	if req.diRegsAll == nil {
		req.diRegsAll = map[string]map[string][]seqDIReg{}
	}
	if req.fieldWriters == nil {
		req.fieldWriters = map[string][]string{}
	}
	if req.fieldTypes == nil {
		req.fieldTypes = map[string][]writeSource{}
	}
	if req.funcRets == nil {
		req.funcRets = map[string][]writeSource{}
	}
	if req.varChains == nil {
		req.varChains = map[string][]writeSource{}
	}
	if req.httpRoutes == nil {
		req.httpRoutes = map[string][]*domain.CodeEntity{}
	}
	if req.httpFiles == nil {
		req.httpFiles = map[string]*seqParsedFile{}
	}
}

// seqIfaceRepoKey 接口缓存键的仓库身份前缀（空 RepoAbs 保持旧键形态，
// 单测/库内直调行为不变）。todo25 §7.3：外层 workspace 请求可能复用同一
// 请求缓存解析多个仓库的同名接口，键必须含仓库身份。
func seqIfaceRepoKey(repoAbs, ifaceID string) string {
	if repoAbs == "" {
		return ifaceID
	}
	return repoAbs + "\x00" + ifaceID
}
