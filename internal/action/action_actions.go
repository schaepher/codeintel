package action

// Actions 用例集合与构造（从 action.go 拆出，控制单文件行数）。

import (
	"sync"

	"github.com/schaepher/codeintel/internal/domain"
)

// Actions 是 CLI 与 HTTP 共享的查询用例集合。
type Actions struct {
	repo     Reader
	modNames []string // 全部 module 路径缓存（P2-3 多 go.mod；modules() 填充）
	// todo13-17：value-trace 结果缓存（按索引构建身份 + 查询参数；nil=未启用）
	vtMu            sync.Mutex
	vtBuild         string
	vtCache         map[string][]*domain.TraceRow
	vtOrder         []string // FIFO 请求键（容量上限淘汰用）
	vtCacheLimit    int      // 0 = vtCacheDefaultLimit
	vtEvictions     int
	vtCacheDisabled bool // A/B 对照：关闭缓存重算
}
