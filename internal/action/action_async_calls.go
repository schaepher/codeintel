package action

// todo13-5：异步关联的调用身份辅助（直接调用者）。
//
// 无字段摘要的 seed 不能把同包全部函数当候选——只保留真实调用/值身份
// 邻居：正向 callee（chainSymbols）+ 反向直接 caller。

import "github.com/schaepher/codeintel/internal/domain"

// directCallersOf id 的直接调用者（calls/grpc_call 边的 source）。
func (a *Actions) directCallersOf(id string) map[string]bool {
	out := map[string]bool{}
	if id == "" {
		return out
	}
	facts, err := a.repo.GetFactsByKinds(string(domain.FactCalls), string(domain.FactGrpcCall))
	if err != nil {
		return out
	}
	for _, f := range facts {
		if string(f.TargetID) == id {
			out[string(f.SourceID)] = true
		}
	}
	return out
}

// directCalleesOf id 的直接被调者（calls/grpc_call 边的 target）。
func (a *Actions) directCalleesOf(id string) map[string]bool {
	out := map[string]bool{}
	if id == "" {
		return out
	}
	facts, err := a.repo.GetFactsByKinds(string(domain.FactCalls), string(domain.FactGrpcCall))
	if err != nil {
		return out
	}
	for _, f := range facts {
		if string(f.SourceID) == id {
			out[string(f.TargetID)] = true
		}
	}
	return out
}
