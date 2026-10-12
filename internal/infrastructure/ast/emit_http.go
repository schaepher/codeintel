package ast

// HTTP 调用识别（从 emitcall.go 拆出——行数治理）。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// emitHTTP §18.7：URL → 路由表匹配 → http_call 边 + http 路由节点。
// httpMethod 由调用方按形态提取（Q205d：Get → GET；NewRequest →
// Args[0]；NewRequestWithContext → Args[1]；Do → 复用 NewRequest 的），
// 空则默认 GET——此前在函数内猜 Args[0] 会把 http.Get(url) 的 URL 当
// method（模块间调用页 label 显示 "http http://..." 噪音）。

func (ctx *fileCtx) emitHTTP(call *ast.CallExpr, callerID domain.CanonicalID, url string, line int, httpMethod, reqType string) {
	host, path := parseURL(url)
	target := ""
	for _, re := range ctx.a.routes {
		if routeMatch(path, re.path) {
			target = string(re.nodeID)
			break
		}
	}
	if target == "" {
		h := host
		if h == "" {
			h = "unknown"
		}
		target = "symbol:http:" + h + ":route." + path
	}
	if httpMethod == "" {
		httpMethod = "GET"
	}
	meta := map[string]any{
		"url":      url,
		"host":     host,
		"path":     path,
		"method":   httpMethod,
		"line_num": line,
	}
	if reqType != "" {
		meta["req_type"] = reqType // R100：请求体实参类型（外部接口判定）
	}
	_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
		ID:         domain.CanonicalID(target),
		Kind:       domain.KindHTTPRoute,
		Name:       "route." + path,
		Properties: map[string]any{"path": path, "method": httpMethod},
	}})
	_ = ctx.emit(domain.Item{Fact: &domain.Fact{
		SourceID:   callerID,
		TargetID:   domain.CanonicalID(target),
		Kind:       domain.FactHTTPCall,
		ToolSource: domain.ToolCodeGraph,
		Confidence: 1.0,
		Metadata:   meta,
	}})
	ctx.httpURLsSeen[url] = true
}

