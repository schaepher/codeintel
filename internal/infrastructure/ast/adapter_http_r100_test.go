package ast

// R100 动态 URL 变量拼接识别测试（待办5：base := "https://..." 局部变量
// 赋值追踪——httpURLString 只认字面量，变量拼接 host 漏检）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestHTTPCallEdgeDynamicURLVar：R100——字符串字面量变量赋值 + 变量拼接
// （base := "https://dyn.example.com" 后 http.Get(base) / http.Get(base+
// "/api") / 链式拼接 url := base + "/v1" / 包级 var）→ host 完整提取。
func TestHTTPCallEdgeDynamicURLVar(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"http/http.go": `package http

func Get(url string) {}
`,
		"svc_a/client.go": `package svc_a

import "example.com/mtest/http"

var baseVar = "http://sms.example.com/send"

func callVar() {
	base := "https://dyn.example.com"
	http.Get(base)
}

func callVarConcat() {
	base := "https://dyn.example.com"
	http.Get(base + "/api/items/" + id())
}

func callVarChain() {
	base := "https://dyn.example.com"
	url := base + "/v1/items"
	http.Get(url)
}

func callPkgVar() {
	http.Get(baseVar)
}

// 非 URL 形态变量不追踪（防 gin 路由/redis key 误伤）
func callNonURL() {
	name := "order"
	http.Get(name)
}

func id() string { return "1" }
`,
	})
	wantHost := map[string]string{
		"callVar":       "dyn.example.com",
		"callVarConcat": "dyn.example.com",
		"callVarChain":  "dyn.example.com",
		"callPkgVar":    "sms.example.com",
	}
	got := map[string]string{}
	for _, f := range facts {
		if f.Kind != domain.FactHTTPCall {
			continue
		}
		src := string(f.SourceID)
		fn := src[strings.LastIndex(src, ":")+1:]
		host, _ := f.Metadata["host"].(string)
		got[fn] = host
	}
	for fn, want := range wantHost {
		if got[fn] != want {
			t.Errorf("%s: host=%q; want %q（变量拼接 host 漏检）", fn, got[fn], want)
		}
	}
	if len(got) != len(wantHost) {
		t.Errorf("http_call 边数量 = %d; want %d（callNonURL 不应建边）", len(got), len(wantHost))
	}
}

// TestHTTPCallEdgeDynamicURLChainPath：链式拼接的 path 完整性。
func TestHTTPCallEdgeDynamicURLChainPath(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"http/http.go": `package http

func Get(url string) {}
`,
		"svc_a/client.go": `package svc_a

import "example.com/mtest/http"

func callChain() {
	base := "https://dyn.example.com"
	url := base + "/v1/items"
	http.Get(url)
}
`,
	})
	var gotURL, gotPath string
	for _, f := range facts {
		if f.Kind == domain.FactHTTPCall {
			gotURL, _ = f.Metadata["url"].(string)
			gotPath, _ = f.Metadata["path"].(string)
		}
	}
	if gotURL != "https://dyn.example.com/v1/items" {
		t.Errorf("链式拼接 url=%q; want https://dyn.example.com/v1/items", gotURL)
	}
	if gotPath != "/v1/items" {
		t.Errorf("链式拼接 path=%q; want /v1/items", gotPath)
	}
}
