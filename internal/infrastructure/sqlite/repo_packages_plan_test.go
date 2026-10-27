package sqlite

// todo21-14.2：包清单查询计划的等价证据。
//
// 原 SQL 的真实计划是 SCAN nodes USING INDEX idx_nodes_name（按名称索引
// 遍历全库、逐节点回表过滤 kind/file_path）。改用 INDEXED BY
// idx_nodes_file_kind 后，kind 与 file_path 谓词都能在窄索引内求值。
// 本回归同时锁定 ①计划确实走窄索引（不再用名称索引）②输出与原谓词、
// 原排序、原去重语义逐项一致。

import (
	"context"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func seedPackageNodes(t *testing.T, r *Repo) {
	t.Helper()
	mk := func(id, kind, name, file string) *domain.CodeEntity {
		return node(id, kind, name, file)
	}
	save(t, r, []*domain.CodeEntity{
		mk("symbol:go:example.com/m/repo:repo", "package", "repo", "repo/repo.go"),
		mk("symbol:go:example.com/m/domain:domain", "package", "domain", "domain/model.go"),
		mk("symbol:go:example.com/m/app:app", "package", "app", "app/app.go"),
		// 被排除的路径前缀（外部/临时/示例/集成/前端）
		mk("symbol:go:example.com/m/x:outside", "package", "outside", "../outside/x.go"),
		mk("symbol:go:example.com/m/tmp:x", "package", "tmp_pkg", "tmp/x.go"),
		mk("symbol:go:example.com/m/integration:x", "package", "integration_pkg", "integration/x.go"),
		mk("symbol:go:example.com/m/examples:x", "package", "examples_pkg", "examples/x.go"),
		mk("symbol:go:example.com/m/e2e:x", "package", "e2e_pkg", "e2e/x.go"),
		mk("symbol:go:example.com/m/skills:x", "package", "skills_pkg", "skills/x.go"),
		// 非 package 节点：不能被包清单选中（且不应触发回表）
		mk("symbol:go:example.com/m/app:Greet", "function", "Greet", "app/app.go"),
		mk("symbol:go:example.com/m/domain:Order", "struct", "Order", "domain/model.go"),
	}, nil)
}

func TestPackagesQueryPlanUsesNarrowIndex(t *testing.T) {
	r := newTestRepo(t)
	seedPackageNodes(t, r)

	rows, err := r.Query("EXPLAIN QUERY PLAN " + packagesQueryIndexed)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	got := plan.String()
	t.Logf("计划:\n%s", got)
	if !strings.Contains(got, "idx_nodes_file_kind") {
		t.Errorf("计划应使用 idx_nodes_file_kind:\n%s", got)
	}
	if strings.Contains(got, "idx_nodes_name") {
		t.Errorf("计划不应再按名称索引全库回表:\n%s", got)
	}
}

// TestPackagesContextMatchesOriginalQuery：新（INDEXED BY）与原语句的
// 输出逐项一致——同过滤、同排序、同去重。
func TestPackagesContextMatchesOriginalQuery(t *testing.T) {
	r := newTestRepo(t)
	seedPackageNodes(t, r)

	got, err := r.GetPackagesContext(context.Background())
	if err != nil {
		t.Fatalf("GetPackagesContext: %v", err)
	}
	rows, err := r.Query(packagesQueryPlain)
	if err != nil {
		t.Fatalf("old query: %v", err)
	}
	defer rows.Close()
	want, err := scanNodes(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 3 {
		t.Fatalf("原语句应返回 3 个仓库内包节点，got %d", len(want))
	}
	if len(got) != len(want) {
		t.Fatalf("新实现 %d 个包节点，原语句 %d 个", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Name != want[i].Name ||
			got[i].FilePath != want[i].FilePath || got[i].Kind != want[i].Kind {
			t.Errorf("第 %d 项不一致: got %+v want %+v", i, got[i], want[i])
		}
	}
	// 排序语义（ORDER BY name）：app < domain < repo
	wantOrder := []string{"app", "domain", "repo"}
	for i, name := range wantOrder {
		if got[i].Name != name {
			t.Errorf("第 %d 项应为 %s（ORDER BY name），got %s", i, name, got[i].Name)
		}
	}
}

// TestPackagesContextFallsBackWithoutIndex：全量构建的 BeginBulkLoad 会
// DROP idx_nodes_file_kind——在此期间包清单查询不得因索引提示而失败
// （回退到无提示等价语句；旧实现没有这个索引依赖）。
func TestPackagesContextFallsBackWithoutIndex(t *testing.T) {
	r := newTestRepo(t)
	seedPackageNodes(t, r)
	if _, err := r.Exec("DROP INDEX IF EXISTS idx_nodes_file_kind"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	pkgs, err := r.GetPackagesContext(context.Background())
	if err != nil {
		t.Fatalf("索引缺失时应回退而不是报 no query solution: %v", err)
	}
	if len(pkgs) != 3 {
		t.Errorf("回退结果应仍是 3 个包，got %d", len(pkgs))
	}
}
