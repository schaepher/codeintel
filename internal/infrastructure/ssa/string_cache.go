package ssa

// 构建级字符串缓存（Q?：重复解析/重复计算治理）。
//
// types.Type.String()（含签名/泛型实例化路径渲染）与 candidateKey
// （接收者类型 + 方法名）在构建期被海量重复调用：同一类型在不同函数/
// 调用点各自 String()，候选键在注册命中与动态派发判定里反复拼。二者对
// 同一 types.Type / *types.Func 都是**纯函数**，故按构建生命周期
// （funcSnapshot，不跨 SSA Program）共享只增缓存；并发 extractor 首次
// 访问用 mutex 串行，命中路径零分配。
//
// 语义保持：cf_call.go 的派发查询仍以字符串键判定（register 命中），
// 只是键的来源改为缓存；空结果（无接收者 / nil 类型）不缓存为 map
// 条目（无复用价值），行为与直接调用一致。

import "go/types"

// typeString 类型字符串缓存。nil 类型返回空串，不走 map。
func (s *funcSnapshot) typeString(t types.Type) string {
	if t == nil {
		return ""
	}
	if s == nil {
		return t.String()
	}
	s.strMu.RLock()
	v, ok := s.typeStrs[t]
	s.strMu.RUnlock()
	if ok {
		return v
	}
	// 未命中：写锁内二次检查后**在锁内**计算——避免并发首次访问重复渲染
	// （todo2-4）。命中路径仍走 RLock（并发、零分配）。
	s.strMu.Lock()
	defer s.strMu.Unlock()
	if v, ok := s.typeStrs[t]; ok {
		return v
	}
	v = t.String()
	if s.typeStrs == nil {
		s.typeStrs = map[types.Type]string{}
	}
	s.typeStrs[t] = v
	return v
}

// candidateKeyOf 候选实现方法去重键缓存（接收者类型 + 方法名）。
func (s *funcSnapshot) candidateKeyOf(fn *types.Func) string {
	if fn == nil {
		return ""
	}
	if s == nil {
		return candidateKey(fn)
	}
	s.strMu.RLock()
	v, ok := s.candKeys[fn]
	s.strMu.RUnlock()
	if ok {
		return v
	}
	s.strMu.Lock()
	defer s.strMu.Unlock()
	if v, ok := s.candKeys[fn]; ok {
		return v
	}
	v = candidateKey(fn)
	if s.candKeys == nil {
		s.candKeys = map[*types.Func]string{}
	}
	s.candKeys[fn] = v
	return v
}

// typeString 提取器侧类型字符串：快照存在走构建级缓存；快照缺失（如
// emitGlobalInit 的轻量提取器）回退直接渲染——不静默返回空串。
func (ext *fieldExtractor) typeString(t types.Type) string {
	if ext == nil || ext.funcs == nil {
		if t == nil {
			return ""
		}
		return t.String()
	}
	return ext.funcs.typeString(t)
}

// candidateKeyOf 提取器侧候选键：同上，快照缺失回退纯函数。
func (ext *fieldExtractor) candidateKeyOf(fn *types.Func) string {
	if ext == nil || ext.funcs == nil {
		return candidateKey(fn)
	}
	return ext.funcs.candidateKeyOf(fn)
}

// typeString aliasPass 侧类型字符串：共享同一构建级缓存（alias 发射与
// extractor 发射是同一批类型，跨 pass 复用）。
func (p *aliasPass) typeString(t types.Type) string {
	if p == nil || p.funcs == nil {
		if t == nil {
			return ""
		}
		return t.String()
	}
	return p.funcs.typeString(t)
}

// snakeCase 字段/类型名 → snake_case 的构建级缓存：snakeCase 对每个名字跑
// ~30 个 initialism 的 ReplaceAll，字段名在大量实体间重复（Id/CreatedAt…），
// 纯函数可复用。命中路径零分配。
func (s *funcSnapshot) snakeCase(name string) string {
	if name == "" {
		return ""
	}
	if s == nil {
		return snakeCase(name)
	}
	s.snakeMu.RLock()
	v, ok := s.snakes[name]
	s.snakeMu.RUnlock()
	if ok {
		return v
	}
	s.snakeMu.Lock()
	defer s.snakeMu.Unlock()
	if v, ok := s.snakes[name]; ok {
		return v
	}
	v = snakeCase(name)
	if s.snakes == nil {
		s.snakes = map[string]string{}
	}
	s.snakes[name] = v
	return v
}

// snakeCase 提取器侧：快照缺失回退纯函数。
func (ext *fieldExtractor) snakeCase(name string) string {
	if ext == nil || ext.funcs == nil {
		return snakeCase(name)
	}
	return ext.funcs.snakeCase(name)
}

// allocTypeShort 构建级类型短名缓存（P1-04）。纯函数按类型串缓存——
// 匿名分配的展示名回退会在同一类型的大量临时值上重复调用。
// 空串不缓存（无复用价值，行为与直接调用一致）。
func (s *funcSnapshot) allocTypeShort(ts string) string {
	if ts == "" {
		return ""
	}
	if s == nil {
		return allocTypeShort(ts)
	}
	s.shortMu.RLock()
	v, ok := s.shorts[ts]
	s.shortMu.RUnlock()
	if ok {
		return v
	}
	s.shortMu.Lock()
	defer s.shortMu.Unlock()
	if v, ok := s.shorts[ts]; ok {
		return v
	}
	v = allocTypeShort(ts)
	if s.shorts == nil {
		s.shorts = map[string]string{}
	}
	s.shorts[ts] = v
	return v
}

// allocTypeShort 提取器侧：快照缺失回退纯函数（同 typeString 口径，
// 不静默返回空串）。
func (ext *fieldExtractor) allocTypeShort(ts string) string {
	if ext == nil || ext.funcs == nil {
		return allocTypeShort(ts)
	}
	return ext.funcs.allocTypeShort(ts)
}

// allocTypeShort aliasPass 侧：共享同一构建级缓存（与字段提取同批类型）。
func (p *aliasPass) allocTypeShort(ts string) string {
	if p == nil || p.funcs == nil {
		return allocTypeShort(ts)
	}
	return p.funcs.allocTypeShort(ts)
}
