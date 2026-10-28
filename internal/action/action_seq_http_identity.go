package action

// T11：HTTP 传输/handler 身份辅助（从 action_seq_http.go 拆出——行数治理）。

import "strings"

// seqHTTPTransportFunc HTTP 方法 → net/http 包级函数名（传输节点身份）。
func seqHTTPTransportFunc(method string) string {
	switch method {
	case "GET":
		return "Get"
	case "HEAD":
		return "Head"
	case "POST":
		return "Post"
	}
	return ""
}

// seqHTTPHandlerActor handler 调用节点参与者（短名；canonical ID 末段）。
func seqHTTPHandlerActor(handlerID string) string {
	rest := strings.TrimPrefix(handlerID, "symbol:go:")
	if i := strings.LastIndex(rest, ":"); i >= 0 && i < len(rest)-1 {
		name := rest[i+1:]
		if j := strings.LastIndex(name, "."); j >= 0 {
			name = name[j+1:]
		}
		return name
	}
	return rest
}
