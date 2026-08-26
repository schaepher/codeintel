package cli

// R17 模块页关键数据流测试：核心符号字段读写聚合（读/写分组去重）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// newTestActions 打开 fixture 仓库的 Actions（db 由调用方关闭）。
func newTestActions(t *testing.T, dir string) (*action.Actions, error) {
	t.Helper()
	db, err := sqlite.Open(dir)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { db.Close() })
	return action.New(sqlite.NewRepo(db)), nil
}

// TestWikiKeyFlows：核心符号的字段读写分组（direct_read 归读、
// direct_write/indirect_write 归写，去重）。
func TestWikiKeyFlows(t *testing.T) {
	dir := seedFieldTrace(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	flows := acts.WikiKeyFlows("example.com/m", []string{"main"})
	if len(flows) == 0 {
		t.Fatal("应有 main 的数据流")
	}
	if flows[0].Symbol != "main" {
		t.Errorf("symbol = %s, want main", flows[0].Symbol)
	}
	// main 写 t.A（:5）、读 t.A（:7）——读 1 写 1
	if len(flows[0].Reads) != 1 || !strings.Contains(flows[0].Reads[0], "T.A") {
		t.Errorf("读 = %v, want [T.A]", flows[0].Reads)
	}
	if len(flows[0].Writes) != 1 || !strings.Contains(flows[0].Writes[0], "T.A") {
		t.Errorf("写 = %v, want [T.A]", flows[0].Writes)
	}
}

// TestWikiKeyFlowsNone：无字段访问的符号 → 空数据流（不报错）。
func TestWikiKeyFlowsNone(t *testing.T) {
	dir := seedRepo(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	flows := acts.WikiKeyFlows("example.com/m", []string{"(Svc).Run"})
	if len(flows) != 0 {
		t.Errorf("(Svc).Run 无字段访问，flows 应为空: %v", flows)
	}
}
