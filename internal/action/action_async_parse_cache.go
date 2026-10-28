package action

// T15：async 边界证明的请求内解析缓存。
//
// 现象：proveAsyncBoundary 对每条身份链各调一次 parseFuncNode——同一
// 生产/消费函数在 N 条链上被**重复读盘 + 全文件解析**（GetSymbol 也重复）。
// 这里按请求建缓存（成功与失败都缓存），请求结束即丢弃（下一请求重新
// 读取——索引/源码可能在两次请求之间变化）。
//
// 缓存键 = 函数 canonical ID；失败（无节点/无文件/解析失败/无函数体）同样
// 记为 miss，避免同一坏符号反复读盘。

// asyncParseCache 一次请求内的函数解析缓存（非并发安全：请求内串行使用）。
type asyncParseCache struct {
	entries map[string]*asyncParseEntry
}

type asyncParseEntry struct {
	fn  *parsedFunc
	hit bool
}

func newAsyncParseCache() *asyncParseCache {
	return &asyncParseCache{entries: map[string]*asyncParseEntry{}}
}

// parseFuncNodeCached 同 parseFuncNode，但结果（含失败）缓存在本次请求。
func (a *Actions) parseFuncNodeCached(pc *asyncParseCache, id string) *parsedFunc {
	if pc == nil {
		return a.parseFuncNode(id)
	}
	if e, ok := pc.entries[id]; ok && e.hit {
		return e.fn
	}
	fn := a.parseFuncNode(id)
	pc.entries[id] = &asyncParseEntry{fn: fn, hit: true}
	return fn
}
