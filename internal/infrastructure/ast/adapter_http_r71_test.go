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

// TestHTTPCallEdgeSprintfURL：R78——fmt.Sprintf 拼 URL（go2o cl253 形态：
// `strUrl := fmt.Sprintf("%s?un=%s...", url常量, ...)` 后 http.Get(strUrl)）
// ——格式串可静态求值的前缀提取（常量参数展开、不可解析参数截断）。
func TestHTTPCallEdgeSprintfURL(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"http/http.go": `package http

func Get(url string) {}
`,
		"svc_a/client.go": `package svc_a

import (
	"fmt"

	"example.com/mtest/http"
)

// go2o cl253 形态：常量 base + Sprintf 拼查询串
const base = "http://sms.253.com/msg/send"

func callSprintf(account, pwd, phone, content string) error {
	strUrl := fmt.Sprintf("%s?un=%s&pw=%s&phone=%s&msg=%s&rd=1",
		base, account, pwd, phone, content)
	http.Get(strUrl)
	return nil
}
`,
	})
	var gotURL, gotHost string
	for _, f := range facts {
		if f.Kind == domain.FactHTTPCall {
			gotURL, _ = f.Metadata["url"].(string)
			gotHost, _ = f.Metadata["host"].(string)
		}
	}
	if gotHost != "sms.253.com" {
		t.Errorf("Sprintf 常量前缀应提取 host: host=%q", gotHost)
	}
	// 完整 URL 前缀：常量参数展开（base）+ 字面量段（?un=）+ 动态参数截断
	if gotURL != "http://sms.253.com/msg/send?un=" {
		t.Errorf("Sprintf 可静态求值前缀 = %q; want http://sms.253.com/msg/send?un=", gotURL)
	}
}
