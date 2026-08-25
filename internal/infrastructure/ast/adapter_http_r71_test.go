package ast

// R71 动态 URL 部分解析测试（从 adapter_http_test.go 拆出——行数治理）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestHTTPCallEdgePartialURL：R71——动态 URL 拼接（"字面量" + 变量）
// 部分解析——提取 host/path 前缀，出站调用不再漏检（go2o sms/
// alipay/geo 形态）。
func TestHTTPCallEdgePartialURL(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"http/http.go": `package http

func Get(url string) {}
`,
		"svc_a/client.go": `package svc_a

import "example.com/mtest/http"

// 动态 URL：字面量前缀 + 变量拼接（盲区形态）
func callDyn() {
	base := "https://dyn.example.com"
	http.Get(base + "/api/items/" + id())
}

func callDyn2() {
	http.Get("https://sms.example.com/send?" + query)
}

func id() string { return "1" }
`,
	})
	var gotHost, gotPath string
	for _, f := range facts {
		if f.Kind == domain.FactHTTPCall {
			gotHost, _ = f.Metadata["host"].(string)
			gotPath, _ = f.Metadata["path"].(string)
		}
	}
	if gotHost != "dyn.example.com" && gotHost != "sms.example.com" {
		t.Errorf("动态 URL 应提取 host（部分解析）: host=%q", gotHost)
	}
	if gotPath == "" {
		t.Errorf("动态 URL 应提取 path 前缀: host=%q path=%q", gotHost, gotPath)
	}
}
