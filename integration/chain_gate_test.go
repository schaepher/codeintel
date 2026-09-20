//go:build integration

package integration

// Q252c 跨函数链完整性门槛（度量实现见 chain_check_test.go）。
//
//	TestChainIntegritySmoke    —— fixtureapp 规模，in-process 秒级，进 make it
//	TestChainIntegrityBaseline —— 大仓（CHAIN_REPO 指定，scripts/chaincheck.sh 驱动）
//
// 门槛口径（docs/field_trace.md §95）：
//   - 0 容忍：一跳对（argument/returns 边两端）经 action.Path 必须 100% 可达
//     ——图里有边却查不到路径＝查询层静默断链（Q252c 的 GetPath 深度 bug）
//   - 基线项：多跳抽样可达率 / alias 连通数 / 分裂候选数（Q252b 类分裂报警）

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestChainIntegritySmoke fixtureapp 冒烟：0 容忍项 + 打印度量（无基线）。
func TestChainIntegritySmoke(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	copyDir(t, "fixtureapp", filepath.Join(dir, "fixtureapp"))
	repoDir := filepath.Join(dir, "fixtureapp")
	clearIndex(t, repoDir)
	if code := runCLI(t, "init", "--repo", repoDir); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	m := measureChain(t, repoDir, "fixtureapp", 50, 30, 4)
	t.Logf("链完整性（smoke）：一跳 %d/%d、多跳 %d/%d、alias 连通 %d、分裂候选 %d",
		m.OneHopOK, m.OneHopSampled, m.MultiHopOK, m.MultiHopSampled, m.AliasConnected, m.SplitCandidates)
	if m.OneHopSampled == 0 {
		t.Fatal("fixtureapp 无 argument/returns 边——fixture 覆盖不足（门槛失去意义）")
	}
	if m.OneHopRate() < 1.0 {
		t.Errorf("一跳对可达率 %.4f（%d/%d），argument/returns 边两端必须 100%% 可达",
			m.OneHopRate(), m.OneHopOK, m.OneHopSampled)
	}
}

// TestChainIntegrityBaseline 大仓基线检查：CHAIN_REPO 指定的已索引仓库。
// 未设 CHAIN_REPO 时跳过（默认 make it 不跑大仓）。
//
//	CHAIN_LABEL      基线标签（默认仓库目录名）
//	CHAIN_ONE_HOP    一跳对抽样数（默认 200）
//	CHAIN_MULTI_HOP  多跳对抽样数（默认 100）
//	CHAIN_HOPS       多跳跳数（默认 4）
//	CHAIN_TOLERANCE  基线容差（默认 0.02）
//	CHAIN_UPDATE=1   写/更新基线文件而不比较
func TestChainIntegrityBaseline(t *testing.T) {
	repoDir := os.Getenv("CHAIN_REPO")
	if repoDir == "" {
		t.Skip("CHAIN_REPO 未设（大仓基线检查用 scripts/chaincheck.sh）")
	}
	label := os.Getenv("CHAIN_LABEL")
	if label == "" {
		label = filepath.Base(repoDir)
	}
	oneHop, multi, hops := 500, 400, 4
	if v := os.Getenv("CHAIN_ONE_HOP"); v != "" {
		oneHop, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("CHAIN_MULTI_HOP"); v != "" {
		multi, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("CHAIN_HOPS"); v != "" {
		hops, _ = strconv.Atoi(v)
	}
	tolerance := 0.02
	if v := os.Getenv("CHAIN_TOLERANCE"); v != "" {
		tolerance, _ = strconv.ParseFloat(v, 64)
	}
	if abs, err := filepath.Abs(repoDir); err == nil {
		repoDir = abs
	}
	m := measureChain(t, repoDir, label, oneHop, multi, hops)
	raw, _ := json.Marshal(m)
	t.Logf("CHAIN-RESULT %s", raw)

	path := chainBaselinePath(label)
	if os.Getenv("CHAIN_UPDATE") == "1" {
		// 耗时不计入基线（机器噪声，会造成无意义 diff）
		m.ElapsedMS = 0
		writeChainBaseline(t, path, m)
		return
	}
	base, err := loadChainBaseline(t, path)
	if err != nil {
		t.Logf("无基线（%s）——只做 0 容忍检查，不比较基线（--update 可写入）", path)
		if m.OneHopRate() < 1.0 {
			t.Errorf("一跳对可达率 %.4f（%d/%d）", m.OneHopRate(), m.OneHopOK, m.OneHopSampled)
		}
		return
	}
	compareChainMetrics(t, m, base, tolerance)
}

// TestCompareChainMetrics 比较规则单测（不依赖大仓——门槛"会咬人"才有效）。
func TestCompareChainMetrics(t *testing.T) {
	base := &chainMetrics{OneHopSampled: 200, OneHopOK: 200, MultiHopSampled: 100,
		MultiHopOK: 100, AliasConnected: 300, SplitCandidates: 800}
	cases := []struct {
		name string
		mut  func(m *chainMetrics)
		want string // 期望命中的问题关键字（"" = 通过）
	}{
		{"全等", func(m *chainMetrics) {}, ""},
		{"一跳下降", func(m *chainMetrics) { m.OneHopOK = 199 }, "一跳对可达率"},
		{"多跳低于基线-容差", func(m *chainMetrics) { m.MultiHopOK = 97 }, "多跳可达率回归"},
		{"多跳在容差内", func(m *chainMetrics) { m.MultiHopOK = 99 }, ""},
		{"alias 连通下降", func(m *chainMetrics) { m.AliasConnected = 280 }, "alias 连通数回归"},
		{"分裂候选上涨", func(m *chainMetrics) { m.SplitCandidates = 1000 }, "分裂候选上涨"},
		{"分裂候选小幅波动", func(m *chainMetrics) { m.SplitCandidates = 815 }, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur := *base
			c.mut(&cur)
			problems := chainProblems(&cur, base, 0.02)
			got := strings.Join(problems, " | ")
			if c.want == "" {
				if got != "" {
					t.Errorf("期望通过，实际报：%s", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("期望报 %q，实际：%s", c.want, got)
			}
		})
	}
}
