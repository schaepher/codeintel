package ast

// todo25 §4.3：同包不同文件的路由节点 ID 唯一性回归。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestHTTPRouteIDsUniqueAcrossFiles：同包不同文件注册的路由节点 ID 必须唯一
// （包 + 仓库相对路径 + 表达式 offset）——旧实现用每文件自增序号，两个文件
// 都发 route.1 → UPSERT 合并成一条，真实 init 后两条注册只剩一条。
func TestHTTPRouteIDsUniqueAcrossFiles(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"api/a.go": `package api

import "net/http"

func a(w http.ResponseWriter, r *http.Request) {}

func setupA() { http.HandleFunc("/a", a) }
`,
		"api/b.go": `package api

import "net/http"

func b(w http.ResponseWriter, r *http.Request) {}

func setupB() { http.HandleFunc("/b", b) }
`,
	})
	ids := map[string]string{}
	for _, n := range nodes {
		if n.Kind != domain.KindHTTPRoute {
			continue
		}
		path := asStr(n.Properties["path"])
		id := string(n.ID)
		if prev, dup := ids[id]; dup {
			t.Fatalf("路由节点 ID 冲突: %s 同时用于 %s 与 %s", id, prev, path)
		}
		ids[id] = path
		if !strings.Contains(id, "api/a.go@") && !strings.Contains(id, "api/b.go@") {
			t.Errorf("路由 ID 应含仓库相对路径与表达式 offset: %s", id)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("应各有 1 条路由（2 个文件），got %d: %v", len(ids), ids)
	}
}
