package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestWikiServe：P2b——wiki 网页版多页路由：
//   - /wiki/ → 302 /wiki/overview
//   - /wiki/overview：概览页（标题 + 模块链接）
//   - /wiki/mod/<短名>：模块页
//   - /wiki/tables：表清单页
//   - /wiki/er：ER 图页（无关联时显示提示）
//   - 未知路径 → 404
func TestWikiServe(t *testing.T) {
	dir := seedWikiRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	h := wikiServeHandler(dir, acts)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// 不跟随重定向（/wiki/ 的 302 Location 断言需要首跳响应）
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(path string) (*http.Response, string) {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		buf := new(strings.Builder)
		if _, err := io.Copy(buf, resp.Body); err != nil {
			t.Fatalf("GET %s read: %v", path, err)
		}
		return resp, buf.String()
	}

	// /wiki/ → 302 /wiki/overview
	resp, _ := get("/wiki/")
	if resp.StatusCode != http.StatusFound {
		t.Errorf("/wiki/ status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/wiki/overview" {
		t.Errorf("/wiki/ Location = %q, want /wiki/overview", loc)
	}

	// 概览页
	resp, body := get("/wiki/overview")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/wiki/overview status = %d", resp.StatusCode)
	}
	for _, want := range []string{"业务 wiki", "/wiki/mod/m", "/wiki/tables", "/wiki/er"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview 应含 %q", want)
		}
	}
	// A：serve 场景侧栏有"图探索"返回链接
	if !strings.Contains(body, "图探索") || !strings.Contains(body, `href="/"`) {
		t.Errorf("overview 应含图探索返回链接")
	}
	// C：跨页搜索索引内嵌（模块/表条目 + 跳转 href）
	for _, want := range []string{"var WIKI_IDX", `"t":"模块"`, "/wiki/mod/m", `"t":"表"`, "/wiki/tables#tbl-orders"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview 应含搜索索引 %q", want)
		}
	}

	// 模块页（短名 m）
	resp, body = get("/wiki/mod/m")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/wiki/mod/m status = %d", resp.StatusCode)
	}
	for _, want := range []string{"主包：业务入口", "(Svc).Run", "orders"} {
		if !strings.Contains(body, want) {
			t.Errorf("模块页应含 %q", want)
		}
	}

	// 表清单页
	resp, body = get("/wiki/tables")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/wiki/tables status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "orders") {
		t.Errorf("表清单页应含 orders")
	}

	// ER 页（无表间关联 → 提示文本）
	resp, body = get("/wiki/er")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/wiki/er status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "无表间直接关联") {
		t.Errorf("ER 页应含无关联提示，body 前 200: %.200s", body)
	}
	// E：模块筛选工具条（全部/各模块链接 + 交互 ER 入口）
	for _, want := range []string{`href="/wiki/er"`, `href="/wiki/er?mod=m"`, "/er.html"} {
		if !strings.Contains(body, want) {
			t.Errorf("ER 页应含筛选工具条 %q", want)
		}
	}
	// E：按模块过滤（m 模块相关表 orders）
	resp, body = get("/wiki/er?mod=m")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/wiki/er?mod=m status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "相关表（1 张") {
		t.Errorf("ER 过滤页应含模块相关表说明，body 前 300: %.300s", body)
	}

	// R1：命令页 / HTTP 接口页
	resp, body = get("/wiki/commands")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "codeintel init") {
		t.Errorf("/wiki/commands status = %d（应含命令清单）", resp.StatusCode)
	}
	resp, body = get("/wiki/api")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "HTTP 接口") {
		t.Errorf("/wiki/api status = %d（应含接口页；内容依赖仓库源码——真实仓库冒烟验证）", resp.StatusCode)
	}
	// R2：系统流程页（依赖索引——fixture 无 cmdInit 符号则仅断言页面存在）
	resp, body = get("/wiki/processes")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "系统流程") {
		t.Errorf("/wiki/processes status = %d（应含流程页）", resp.StatusCode)
	}

	// 未知路径 → 404
	resp, _ = get("/wiki/unknown")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/wiki/unknown status = %d, want 404", resp.StatusCode)
	}
}

// TestWikiServeYAML：仓库根 wiki.yaml 自动加载（模块描述出现在概览页）。
func TestWikiServeYAML(t *testing.T) {
	dir := seedWikiRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "wiki.yaml"), []byte(`
project:
  description: 示例业务系统
modules:
  - name: example.com/m
    description: 核心模块说明
`), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))
	srv := httptest.NewServer(wikiServeHandler(dir, acts))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/wiki/overview")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"示例业务系统", "核心模块说明"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("概览页应含 yaml 描述 %q", want)
		}
	}
}
