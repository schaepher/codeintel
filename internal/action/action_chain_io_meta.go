package action

import "strings"

// grpcSvcNames 全部 grpc 服务名集合（内存——grpc 客户端判定）。
func grpcSvcNames(a *Actions) map[string]bool {
	out := map[string]bool{}
	svcs, err := a.repo.GetGrpcServices()
	if err != nil {
		return out
	}
	for _, n := range svcs {
		if name := n.Property("service_name"); name != "" {
			out[name] = true
		}
	}
	return out
}

// uniqueSvcID 服务名 → 唯一服务节点 ID（多个同名时返回空——不猜）。
func uniqueSvcID(name string, a *Actions) string {
	ids := map[string]bool{}
	svcs, err := a.repo.GetGrpcServices()
	if err != nil {
		return ""
	}
	for _, n := range svcs {
		if n.Property("service_name") == name {
			ids[string(n.ID)] = true
		}
	}
	if len(ids) != 1 {
		return ""
	}
	for id := range ids {
		return id
	}
	return ""
}

// grpcClientName 被调符号是否为 grpc 客户端类型——短名 `<Svc>Client`
// 且 Svc 是已知 grpc 服务名（内存判定）。返回服务名。
func grpcClientName(symID string, svcs map[string]bool) string {
	rest := strings.TrimPrefix(symID, "symbol:go:")
	name := rest
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		name = rest[i+1:]
	}
	if !strings.HasSuffix(name, "Client") {
		return ""
	}
	svc := strings.TrimSuffix(name, "Client")
	if svc != "" && svcs[svc] {
		return svc
	}
	return ""
}

// metaLine metadata line_num 提取（JSON 反序列化为 float64）。
func metaLine(m map[string]any) int {
	if v, ok := m["line_num"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}
