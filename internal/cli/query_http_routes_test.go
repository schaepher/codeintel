package cli

// R92 测试：cmdHTTPRoutes 转发（查询逻辑在 action——Actions.HTTPRoutes
// 已单独测试）；cli 只做参数转发 + 输出。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestCmdHTTPRoutesForward：命令行转发——JSON 输出含路由契约字段。
func TestCmdHTTPRoutesForward(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/api:route.1", Kind: domain.KindHTTPRoute,
			Name: "GET /ping", Properties: map[string]any{
				"method": "GET", "path": "/ping", "handler": "pingHandler",
				"resolver": "gin", "register": "api/routes.go:20",
			}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(func() {
		if code := cmdHTTPRoutes(action.New(sqlite.NewRepo(db)), queryFlags{json: true}); code != 0 {
			t.Fatalf("cmdHTTPRoutes exit = %d", code)
		}
	})
	for _, want := range []string{`"routes"`, `"GET"`, `"pingHandler"`} {
		if !strings.Contains(out, want) {
			t.Errorf("命令输出应含 %s:\n%s", want, out)
		}
	}
}
