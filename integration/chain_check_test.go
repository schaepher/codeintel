//go:build integration

package integration

// Q252c 跨函数链完整性门槛的核心度量（测试见 chain_gate_test.go）。
//
// 为什么需要：Q252b 抓到的两个真 bug 都属于"图/查询层静默断链"——
//   ① 同一 SSA 值分裂成两个节点 → alias 边与 argument/returns 边分家
//   ② GetPath 把 maxDepth 当**节点预算** → 可达的两点报"无路径"
// 两者都不会让任何既有断言变红，只会在用户查询时表现为"链断了"。
// 这里把"链完整性"变成可复现的数字门槛：
//
//   0 容忍项：一跳对（argument/returns 边两端）经 action.Path 必须 100% 可达
//   基线项  ：多跳抽样可达率 / alias 连通数 / 分裂候选数（Q252b 类回归报警）
//
// 抽样确定性：按 id 排序后等步长取（可复现，不依赖 map 迭代顺序）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// chainMetrics 一次链完整性检查的全部度量（基线文件即此结构）。
type chainMetrics struct {
	Repo            string `json:"repo"`               // 仓库标签（基线文件名用）
	Nodes           int    `json:"nodes"`              // 节点总数（参考）
	Edges           int    `json:"edges"`              // 边总数（参考）
	SSAValues       int    `json:"ssa_values"`         // ssa_value 节点数（参考）
	SSAValuesNoLine int    `json:"ssa_values_no_line"` // 其中无行号者（alias pass 发的）
	OneHopSampled   int    `json:"one_hop_sampled"`    // 一跳对抽样数
	OneHopOK        int    `json:"one_hop_ok"`         // 其中可达（必须 == 抽样数）
	MultiHopSampled int    `json:"multi_hop_sampled"`
	MultiHopOK      int    `json:"multi_hop_ok"`
	AliasConnected  int    `json:"alias_connected"`  // alias 边落点同时挂数据流/传参边
	SplitCandidates int    `json:"split_candidates"` // Q252b 类分裂候选（越小越好）
	ElapsedMS       int    `json:"elapsed_ms"`
}

// OneHopRate 一跳对可达率。
func (m *chainMetrics) OneHopRate() float64 {
	if m.OneHopSampled == 0 {
		return 0
	}
	return float64(m.OneHopOK) / float64(m.OneHopSampled)
}

// MultiHopRate 多跳抽样可达率。
func (m *chainMetrics) MultiHopRate() float64 {
	if m.MultiHopSampled == 0 {
		return 0
	}
	return float64(m.MultiHopOK) / float64(m.MultiHopSampled)
}

// measureChain 打开已索引仓库并跑完整度量（不构建索引）。
func measureChain(t *testing.T, repoDir, label string, oneHopN, multiHopN, hops int) *chainMetrics {
	t.Helper()
	start := time.Now()
	db, err := sqlite.Open(repoDir)
	if err != nil {
		t.Fatalf("打开索引 %s: %v", repoDir, err)
	}
	defer db.Close()
	repo := sqlite.NewRepo(db)
	m := &chainMetrics{Repo: label}
	m.Nodes = countQuery(t, repo, `SELECT count(*) FROM nodes`)
	m.Edges = countQuery(t, repo, `SELECT count(*) FROM edges`)
	m.SSAValues = countQuery(t, repo, `SELECT count(*) FROM nodes WHERE kind='ssa_value'`)
	m.SSAValuesNoLine = countQuery(t, repo, `SELECT count(*) FROM nodes WHERE kind='ssa_value' AND COALESCE(line_start,0)=0`)
	m.AliasConnected = countQuery(t, repo, `SELECT count(*) FROM edges e JOIN nodes n ON n.id=e.target_id
		WHERE e.kind='alias' AND EXISTS (SELECT 1 FROM edges e2 WHERE e2.source_id=n.id
			AND e2.kind IN ('argument','returns','data_flows_to'))`)
	// 分裂候选：同 (func_id, type_string, lower(ssa_op)) 且行号存在性互补——
	// Q252b 的签名（同一指令被两条发射路径写成两个节点）。不是 0 容忍项
	// （fe 的 load/alloc 分支与 alias pass 的合法共存会产生少量命中），
	// 作为基线项：修复前 go2o 3101 → 修复后 892，回涨即刻可见。
	m.SplitCandidates = countQuery(t, repo, `SELECT count(*) FROM nodes n WHERE n.kind='ssa_value'
		AND COALESCE(n.line_start,0)=0
		AND EXISTS (SELECT 1 FROM nodes m WHERE m.kind='ssa_value' AND m.id<>n.id
			AND json_extract(m.properties,'$.func_id')=json_extract(n.properties,'$.func_id')
			AND json_extract(m.properties,'$.type_string')=json_extract(n.properties,'$.type_string')
			AND lower(json_extract(m.properties,'$.ssa_op'))=lower(json_extract(n.properties,'$.ssa_op'))
			AND COALESCE(m.line_start,0)>0)`)

	acts := action.New(repo)
	// 一跳对：argument/returns 边两端（查询边集内的最小单元——必须可达）
	oneHop := sampleChainEdges(t, repo, `kind IN ('argument','returns')`, oneHopN)
	for _, p := range oneHop {
		if chainReachable(t, acts, p[0], p[1]) {
			m.OneHopOK++
		} else {
			t.Logf("一跳对不可达（图内有 %s → %s 边，Path 却报无路径）", p[0], p[1])
		}
	}
	m.OneHopSampled = len(oneHop)
	// 多跳对：沿查询端同一批边集走 hops 跳（原始图内存在路径）
	multi := multiHopChainPairs(t, repo, hops, multiHopN)
	for _, p := range multi {
		if chainReachable(t, acts, p[0], p[1]) {
			m.MultiHopOK++
		}
	}
	m.MultiHopSampled = len(multi)
	m.ElapsedMS = int(time.Since(start).Milliseconds())
	return m
}

// chainReachable 数据流边集下 from→to 是否可达（深度上限 8；渲染无关）。
func chainReachable(t *testing.T, acts *action.Actions, from, to string) bool {
	t.Helper()
	rows, err := acts.Path(from, to, 8, false)
	if err != nil {
		t.Fatalf("Path(%s,%s): %v", from, to, err)
	}
	return len(rows) > 0
}

func countQuery(t *testing.T, repo *sqlite.Repo, q string) int {
	t.Helper()
	rows, err := repo.Query(q)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return 0
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan %q: %v", q, err)
	}
	return n
}

// sampleChainEdges 确定性抽样边（按 (source,target) 排序后等步长取 n 条）。
func sampleChainEdges(t *testing.T, repo *sqlite.Repo, where string, n int) [][2]string {
	t.Helper()
	rows, err := repo.Query(`SELECT source_id, target_id FROM edges WHERE ` + where + ` ORDER BY source_id, target_id`)
	if err != nil {
		t.Fatalf("sample edges: %v", err)
	}
	defer rows.Close()
	var all [][2]string
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			t.Fatalf("scan edge: %v", err)
		}
		all = append(all, [2]string{a, b})
	}
	if len(all) <= n {
		return all
	}
	step := len(all) / n
	var out [][2]string
	for i := 0; i < len(all) && len(out) < n; i += step {
		out = append(out, all[i])
	}
	return out
}

// multiHopChainPairs 沿查询端边集走 hops 跳，返回确定性抽样的 (起点, 到达点) 对
// ——每一对在原始图里都**存在**一条 ≤ hops 跳的路径，因此 Path 必须可达。
// 只收 ≥2 跳的对（1 跳由 0 容忍项覆盖；浅对测不出 BFS 扩展预算类问题）。
func multiHopChainPairs(t *testing.T, repo *sqlite.Repo, hops, n int) [][2]string {
	t.Helper()
	rows, err := repo.Query(`SELECT source_id, target_id FROM edges
		WHERE kind IN ('data_flows_to','argument','returns','phi_operand','summary_io')
		ORDER BY source_id, target_id`)
	if err != nil {
		t.Fatalf("multi-hop edges: %v", err)
	}
	adj := map[string][]string{}
	starts := map[string]bool{}
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			t.Fatalf("scan multi-hop edge: %v", err)
		}
		adj[a] = append(adj[a], b)
		starts[a] = true
	}
	rows.Close()
	sorted := make([]string, 0, len(starts))
	for s := range starts {
		sorted = append(sorted, s)
	}
	sort.Strings(sorted)
	if n <= 0 || len(sorted) == 0 {
		return nil
	}
	step := len(sorted)/n + 1
	var out [][2]string
	for i := 0; i < len(sorted) && len(out) < n; i += step {
		origin := sorted[i]
		cur := []string{origin}
		seen := map[string]bool{origin: true}
		for h := 0; h < hops && len(out) < n && len(cur) > 0; h++ {
			var next []string
			for _, s := range cur {
				for _, d := range adj[s] {
					if !seen[d] {
						seen[d] = true
						// 只收 ≥2 跳的对：1 跳对由 0 容忍项覆盖，而浅对
						// 测不出 BFS 的扩展预算问题（Q252c 的 GetPath bug
						// 只在发现面变宽后暴露——实测 1 跳对全过、深对失败）
						if h+1 >= 2 {
							out = append(out, [2]string{origin, d})
						}
						next = append(next, d)
					}
				}
			}
			cur = next
		}
	}
	return out
}

// chainBaselinePath 基线文件路径（仓库标签 → scripts/baselines/chain-<label>.json）。
func chainBaselinePath(label string) string {
	base := os.Getenv("CHAIN_BASELINE_DIR")
	if base == "" {
		base = filepath.Join("..", "scripts", "baselines")
	}
	return filepath.Join(base, "chain-"+label+".json")
}

func loadChainBaseline(t *testing.T, path string) (*chainMetrics, error) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m chainMetrics
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s 解析失败: %w", path, err)
	}
	return &m, nil
}

func writeChainBaseline(t *testing.T, path string, m *chainMetrics) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建基线目录: %v", err)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("序列化基线: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("写基线 %s: %v", path, err)
	}
	t.Logf("已更新基线：%s", path)
}

// chainProblems 与基线比较：返回全部违反项（空 = 通过）。纯函数——比较
// 规则本身要被单测覆盖（门槛"会咬人"才有效）。
func chainProblems(cur, base *chainMetrics, tolerance float64) []string {
	var out []string
	if r := cur.OneHopRate(); r < 1.0 {
		out = append(out, fmt.Sprintf("一跳对可达率 %.4f（%d/%d）——argument/returns 边两端必须 100%% 可达",
			r, cur.OneHopOK, cur.OneHopSampled))
	}
	if cur.MultiHopRate() < base.MultiHopRate()-tolerance {
		out = append(out, fmt.Sprintf("多跳可达率回归：%.4f（%d/%d）< 基线 %.4f − %.2f",
			cur.MultiHopRate(), cur.MultiHopOK, cur.MultiHopSampled, base.MultiHopRate(), tolerance))
	}
	if cur.AliasConnected < int(float64(base.AliasConnected)*(1-tolerance)) {
		out = append(out, fmt.Sprintf("alias 连通数回归：%d < 基线 %d − %.0f%%（别名边与数据流边脱连＝Q252b 类分裂）",
			cur.AliasConnected, base.AliasConnected, tolerance*100))
	}
	if cur.SplitCandidates > int(float64(base.SplitCandidates)*(1+tolerance))+5 {
		out = append(out, fmt.Sprintf("分裂候选上涨：%d > 基线 %d + %.0f%%（同一 SSA 值可能又被两条路径写成分裂节点）",
			cur.SplitCandidates, base.SplitCandidates, tolerance*100))
	}
	return out
}

// compareChainMetrics 打印全部违反项（无基线时只查 0 容忍项）。
func compareChainMetrics(t *testing.T, cur *chainMetrics, base *chainMetrics, tolerance float64) {
	t.Helper()
	if base == nil {
		base = &chainMetrics{}
	}
	for _, p := range chainProblems(cur, base, tolerance) {
		t.Errorf("%s", p)
	}
}
