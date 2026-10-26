package action

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// asyncRegistration 一次真实注册绑定的角色证据。
type asyncRegistration struct {
	role     string
	api      string // 注册 API 短名
	evidence string // registration:<API>@<file>:<line>
}

// asyncRegistrationIndex 入口注册证据（todo13-6）：必须有**真实回调绑定**
// （passes_to: 注册 API → 入口函数）且注册 API 有实际调用点（calls 边 +
// 文件/行号）。仅"调用方名字含 schedule"不算注册（普通函数调用不升级）。
func (a *Actions) asyncRegistrationIndex() map[string]asyncRegistration {
	out := map[string]asyncRegistration{}
	passes, err := a.repo.GetFactsByKinds(string(domain.FactPassesTo))
	if err != nil {
		return out
	}
	calls, err := a.repo.GetFactsByKinds(string(domain.FactCalls))
	if err != nil {
		return out
	}
	type site struct {
		file string
		line int
	}
	sites := map[string]site{}
	for _, c := range calls {
		api := string(c.TargetID)
		if _, ok := sites[api]; ok {
			continue
		}
		line := metaLine(c.Metadata)
		if line <= 0 {
			continue
		}
		file := ""
		if n, nerr := a.repo.GetSymbol(c.SourceID); nerr == nil && n != nil {
			file = n.FilePath
		}
		sites[api] = site{file: file, line: line}
	}
	for _, p := range passes {
		api := string(p.SourceID)
		entry := string(p.TargetID)
		role, ok := asyncRoleFromRegistrationAPI(api)
		if !ok {
			continue
		}
		st, ok := sites[api]
		if !ok {
			continue
		}
		ev := "registration:" + shortNameOf(api) + "@" + st.file + ":" + itoaSeq(st.line)
		if prev, exists := out[entry]; !exists || role == AsyncRoleCron {
			_ = prev
			out[entry] = asyncRegistration{role: role, api: shortNameOf(api), evidence: ev}
		}
	}
	return out
}

// asyncRoleFromRegistrationAPI 注册 API → 角色（按真实 API 语义，不看调用方名）。
// 只识别明确的调度/消费注册；http handler 等普通回调不升级生命周期角色。
func asyncRoleFromRegistrationAPI(apiID string) (string, bool) {
	n := strings.ToLower(shortNameOf(apiID))
	if i := strings.LastIndex(n, ")."); i >= 0 {
		n = n[i+2:]
	}
	switch {
	case strings.Contains(n, "subscribe") || strings.Contains(n, "consume") ||
		strings.Contains(n, "listener"):
		return AsyncRoleConsumer, true
	case strings.Contains(n, "addfunc") || strings.Contains(n, "addjob") ||
		strings.Contains(n, "schedule") || strings.Contains(n, "afterfunc") ||
		strings.Contains(n, "ticker") || strings.Contains(n, "every"):
		return AsyncRoleCron, true
	case strings.Contains(n, "register") &&
		(strings.Contains(n, "job") || strings.Contains(n, "task") || strings.Contains(n, "cron")):
		return AsyncRoleCron, true
	case strings.Contains(n, "handlefunc") || strings.Contains(n, "handler"):
		return "", false
	}
	return "", false
}
