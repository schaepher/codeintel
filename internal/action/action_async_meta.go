package action

import "strings"

// asyncRoleRule 角色判定规则（先匹配先返回；只按名字，必须标注为启发式）。
var asyncRoleRules = []struct {
	role string
	keys []string
}{
	{AsyncRoleConsumer, []string{"consume", "listener", "subscribe", "onmessage", "handlemsg"}},
	{AsyncRoleCron, []string{"cron", "timer", "tick", "sweep", "schedule", "job"}},
	{AsyncRoleAudit, []string{"audit", "review", "check", "verify"}},
	{AsyncRoleHuman, []string{"approve", "manual", "human", "confirm", "byhuman"}},
	{AsyncRoleAuto, []string{"auto", "system", "bysystem", "advance", "apply"}},
}

// asyncClassifyRole 按名字判定角色（返回 role + 命中的规则词）。
func asyncClassifyRole(name string) (role, rule string) {
	lower := strings.ToLower(name)
	for _, r := range asyncRoleRules {
		for _, k := range r.keys {
			if strings.Contains(lower, k) {
				return r.role, "name:" + k
			}
		}
	}
	return AsyncRoleOther, "name:none"
}

// asyncSharesType 字段的类型前缀是否属于 seed 的类型集合（共享作用域）。
func asyncSharesType(field string, seedTypes map[string]bool) bool {
	for t := range seedTypes {
		if strings.HasPrefix(field, t+".") {
			return true
		}
	}
	return false
}

// asyncIDColumns seed 字段里的 ID 类列（*_id/*_no/*_code/Id/No/Code）。
func asyncIDColumns(fields map[string]bool) map[string]bool {
	out := map[string]bool{}
	for p := range fields {
		leaf := p
		if i := strings.LastIndex(p, "."); i >= 0 {
			leaf = p[i+1:]
		}
		l := strings.ToLower(leaf)
		if strings.HasSuffix(l, "id") || strings.HasSuffix(l, "no") ||
			strings.HasSuffix(l, "code") || strings.HasSuffix(l, "_id") ||
			strings.HasSuffix(l, "_no") || strings.HasSuffix(l, "_code") {
			out[l] = true
		}
	}
	return out
}

// asyncSharesIDColumn 字段是否为同源 ID 列（同名 ID 类字段——仍需链路验证）。
func asyncSharesIDColumn(field string, cols map[string]bool) bool {
	if len(cols) == 0 {
		return false
	}
	leaf := field
	if i := strings.LastIndex(field, "."); i >= 0 {
		leaf = field[i+1:]
	}
	return cols[strings.ToLower(leaf)]
}

// asyncInScope 字段是否落在同一作用域（seed 字段集合，或名的近似匹配）。
func asyncInScope(field string, seedFields map[string]bool, scope string) bool {
	if scope != "" {
		return strings.Contains(strings.ToLower(field), strings.ToLower(scope))
	}
	typePrefix := asyncScopeOf(seedFields)
	if typePrefix == "" {
		return false
	}
	return strings.HasPrefix(field, typePrefix)
}

// asyncScopeOf 字段路径的公共类型前缀（去掉末段字段名）——表/类型近似。
func asyncScopeOf(fields map[string]bool) string {
	prefix := ""
	for p := range fields {
		i := strings.LastIndex(p, ".")
		if i <= 0 {
			continue
		}
		pre := p[:i]
		if prefix == "" {
			prefix = pre
			continue
		}
		for !strings.HasPrefix(pre, prefix) && prefix != "" {
			j := strings.LastIndex(prefix, ".")
			if j < 0 {
				prefix = ""
				break
			}
			prefix = prefix[:j]
		}
	}
	return prefix
}
