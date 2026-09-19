//go:build integration

package integration

// Q250 图构建确定性门槛（CI 级）：同一 fixture 仓库构建两次，产物必须
// 完全一致——节点 ID 集合与内容（properties 键序归一化）、边集合含 count、
// 摘要行。
//
// 为什么需要这条（go2o 级口径太慢）：Q249 前同二进制双跑（workers=1）
// 差异 nodes 1670 / 摘要 136 / alias 边 492 行，且**与并发无关**——根因是
// map 迭代顺序（Go 每次随机）决定"谁先认领裸槽位名"与间接写传播先后
// （先到先得）。修复后 go2o 双跑四类 dump 全 0（workers=1 与 8）。
//
// 本测试用 fixtureapp（固定真实形态库）作 CI 代理：同进程构建两次
// （Go 的 map 顺序每次不同），任何重新引入的顺序依赖都会让它变红。

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// determinismDump 一次构建的四类产物（排序后）。
type determinismDump struct {
	nodes []string
	edges []string
	summ  []string
}

func dumpFixtureDB(t *testing.T, repoDir string) *determinismDump {
	t.Helper()
	db, err := sqlite.Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := sqlite.NewRepo(db)
	out := &determinismDump{}
	collect := func(query string, dst *[]string) {
		rows, err := repo.Query(query)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("scan: %v", err)
			}
			*dst = append(*dst, s)
		}
	}
	collect(`SELECT id || '|' || kind || '|' || name || '|' || coalesce(file_path,'') || '|' ||
		coalesce(line_start,0) || '|' || coalesce(line_end,0) || '|' || coalesce(properties,'')
		FROM nodes ORDER BY id`, &out.nodes)
	collect(`SELECT source_id || '|' || target_id || '|' || kind || '|' || count
		FROM edges ORDER BY source_id, target_id, kind`, &out.edges)
	collect(`SELECT function_id || '|' || access_kind || '|' || field_path || '|' ||
		coalesce(instance_path,'') || '|' || coalesce(line_start,0) || '|' || coalesce(code_snippet,'')
		FROM function_field_summary ORDER BY function_id, access_kind, field_path`, &out.summ)
	// properties 键序归一化（sqlite json_patch 与 json v2 的序列化细节，
	// 无查询语义——Q250 明确排除在"确定性"定义之外）
	for i, n := range out.nodes {
		parts := strings.SplitN(n, "|", 7)
		if len(parts) < 7 {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(parts[6]), &v); err != nil {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		out.nodes[i] = strings.Join(parts[:6], "|") + "|" + string(b)
	}
	sort.Strings(out.nodes)
	sort.Strings(out.edges)
	sort.Strings(out.summ)
	return out
}

func diffSet(a, b []string) []string {
	set := map[string]int{}
	for _, x := range a {
		set[x]++
	}
	for _, x := range b {
		set[x]--
	}
	var out []string
	for x, c := range set {
		if c != 0 {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// TestBuildDeterminismFixtureApp：同仓库两次 init → 四类产物全等。
func TestBuildDeterminismFixtureApp(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	copyDir(t, "fixtureapp", dir+"/fixtureapp")
	repoDir := dir + "/fixtureapp"

	var dumps []*determinismDump
	for i := 0; i < 2; i++ {
		if code := runCLI(t, "init", "--repo", repoDir); code != 0 {
			t.Fatalf("init #%d exit = %d", i+1, code)
		}
		dumps = append(dumps, dumpFixtureDB(t, repoDir))
	}
	a, b := dumps[0], dumps[1]
	if len(a.nodes) == 0 {
		t.Fatal("fixtureapp 未产出节点（用例失效）")
	}
	for _, c := range []struct {
		name string
		a, b []string
	}{
		{"节点", a.nodes, b.nodes},
		{"边", a.edges, b.edges},
		{"摘要", a.summ, b.summ},
	} {
		if d := diffSet(c.a, c.b); len(d) > 0 {
			t.Errorf("%s 两次构建不一致（%d 条差异，共 %d/%d）：\n  %v",
				c.name, len(d), len(c.a), len(c.b), firstN(d, 3))
		}
	}
	t.Logf("确定性门槛通过：节点 %d、边 %d、摘要 %d", len(a.nodes), len(a.edges), len(a.summ))
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
