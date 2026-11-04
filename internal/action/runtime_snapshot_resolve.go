package action

// T14b：运行时快照输入统一解析（路径/已解析对象/一致性校验）。

import "fmt"

// ResolveRuntimeSnapshot 统一解析运行时输入（T14）：
//   - 两个**路径**入口（RuntimeDataPath / RuntimeData）不一致 → 明确失败，
//     不静默择一；
//   - 已解析快照（Runtime，typed-nil = 未配置）与路径同时给出时必须 hash
//     一致，否则失败；
//   - 都没有 → (nil, nil)（调用方保留原有 error 分支/不额外求值）。
//
// 错误统一用 `runtime-data:` 前缀（只出现一次）。
func (r CodeSequenceRequest) ResolveRuntimeSnapshot() (*RuntimeSnapshot, error) {
	p1 := r.RuntimeDataPath
	p2, obj, err := runtimeDataInput(r.RuntimeData)
	if err != nil {
		return nil, err
	}
	if p1 != "" && p2 != "" && p1 != p2 {
		return nil, fmt.Errorf("runtime-data: 两个路径入口不一致（RuntimeDataPath=%q / RuntimeData=%q）", p1, p2)
	}
	path := p1
	if path == "" {
		path = p2
	}
	// 已解析快照：Runtime（专用字段）与 RuntimeData 对象入口同时给出时必须
	// 同一 hash（不同对象不得静默择一）。
	if r.Runtime != nil && obj != nil && r.Runtime.Hash != obj.Hash {
		return nil, fmt.Errorf("runtime-data: 两个已解析快照不一致（Runtime hash %s != RuntimeData hash %s）",
			r.Runtime.Hash, obj.Hash)
	}
	resolved := obj
	if resolved == nil {
		resolved = r.Runtime
	}
	if resolved != nil {
		if path == "" {
			return resolved, nil
		}
		fromPath, err := LoadRuntimeSnapshot(path)
		if err != nil {
			return nil, err
		}
		if fromPath.Hash != resolved.Hash {
			return nil, fmt.Errorf("runtime-data: 已解析快照与路径快照不一致（hash %s != %s）",
				resolved.Hash, fromPath.Hash)
		}
		return resolved, nil
	}
	if path == "" {
		return nil, nil
	}
	return LoadRuntimeSnapshot(path)
}
