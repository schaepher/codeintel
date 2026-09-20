package sqlite

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q254：VACUUM 判定（纯函数）——freelist 小于阈值/比例不足/磁盘不够都不该跑。
func TestDecideVacuum(t *testing.T) {
	const gb = int64(1) << 30
	cases := []struct {
		name                string
		dbSize, free, disk  int64
		wantRun             bool
		wantReasonSubstring string
	}{
		{"freelist 为空（全量构建后常态）", 13 * gb, 0, 100 * gb, false, "无收益"},
		{"freelist 超阈值且磁盘充足", 13 * gb, 2 * gb, 100 * gb, true, "磁盘充足"},
		{"freelist 低于绝对阈值", 13 * gb, 100 << 20, 100 * gb, false, "收益不足"},
		{"freelist 低于 5% 比例", 13 * gb, 500 << 20, 100 * gb, false, "收益不足"},
		{"磁盘余量不足（13GB 库 + 14GB 空闲）", 13 * gb, 2 * gb, 14 * gb, false, "磁盘余量不足"},
	}
	for _, c := range cases {
		got := decideVacuum(c.dbSize, c.free, c.disk)
		if got.Run != c.wantRun {
			t.Errorf("%s: Run=%v want %v（reason=%s）", c.name, got.Run, c.wantRun, got.Reason)
		}
		if c.wantReasonSubstring != "" && !strings.Contains(got.Reason, c.wantReasonSubstring) {
			t.Errorf("%s: reason=%q 不含 %q", c.name, got.Reason, c.wantReasonSubstring)
		}
	}
}

// Q254：freelist 达到阈值时 VacuumIfWorthwhile 真的回收（阈值在测试里调小）。
func TestVacuumIfWorthwhileReclaims(t *testing.T) {
	oldMin := vacuumMinFreeBytes
	vacuumMinFreeBytes = 1 // 测试阈值：只要 freelist 非空就跑
	defer func() { vacuumMinFreeBytes = oldMin }()

	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	repo := NewRepo(db)
	for i := 0; i < 400; i++ {
		save(t, repo, []*domain.CodeEntity{{
			ID: domain.CanonicalID(fmt.Sprintf("symbol:go:x:padding%d", i)), Kind: domain.KindFunction,
			Name: fmt.Sprintf("padding%d", i), FilePath: "pad.go", Properties: map[string]any{"pad": padStr()},
		}}, nil)
	}
	if _, err := db.Exec("DELETE FROM nodes"); err != nil {
		t.Fatal(err)
	}
	plan, err := db.PlanVacuum()
	if err != nil {
		t.Fatal(err)
	}
	if plan.FreeListBytes <= 0 {
		t.Fatalf("删除后应有 freelist：%+v", plan)
	}
	plan, err = db.VacuumIfWorthwhile("test")
	if err != nil {
		t.Fatalf("VacuumIfWorthwhile: %v", err)
	}
	if !plan.Run {
		t.Fatalf("应执行 VACUUM：%+v", plan)
	}
	after, err := db.PlanVacuum()
	if err != nil {
		t.Fatal(err)
	}
	if after.FreeListBytes != 0 {
		t.Errorf("VACUUM 后 freelist 应为 0：%d", after.FreeListBytes)
	}
}

// Q254 迁移：冗余索引删除（幂等、不影响数据），且新库不再创建它们。
func TestDropRedundantEdgeIndexes(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	repo := NewRepo(db)
	save(t, repo, []*domain.CodeEntity{
		{ID: "symbol:go:x:a", Kind: domain.KindFunction, Name: "a"},
		{ID: "symbol:go:x:b", Kind: domain.KindFunction, Name: "b"},
	}, []*domain.Fact{{SourceID: "symbol:go:x:a", TargetID: "symbol:go:x:b", Kind: domain.FactCalls}})

	// 新库不该有冗余索引
	for _, idx := range redundantEdgeIndexes {
		if hasIndex(t, db, idx) {
			t.Errorf("新建库不应存在 %s（schema.go 已移除）", idx)
		}
	}
	// 必需的索引仍在
	for _, idx := range []string{"idx_edges_kind", "idx_edges_target_kind"} {
		if !hasIndex(t, db, idx) {
			t.Errorf("必需索引 %s 缺失", idx)
		}
	}

	// 模拟旧库：手工建回冗余索引 → 再跑迁移
	for _, idx := range redundantEdgeIndexes {
		stmt := "CREATE INDEX " + idx + " ON edges(source_ref)"
		switch idx {
		case "idx_edges_source_kind":
			stmt = "CREATE INDEX " + idx + " ON edges(source_ref, kind)"
		case "idx_edges_target":
			stmt = "CREATE INDEX " + idx + " ON edges(target_ref)"
		case "idx_edges_confidence":
			stmt = "CREATE INDEX " + idx + " ON edges(confidence) WHERE confidence >= 0.8"
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("建旧索引 %s: %v", idx, err)
		}
	}
	dropped, err := dropRedundantEdgeIndexes(db)
	if err != nil {
		t.Fatalf("dropRedundantEdgeIndexes: %v", err)
	}
	if len(dropped) != len(redundantEdgeIndexes) {
		t.Fatalf("应删 %d 个，实删 %v", len(redundantEdgeIndexes), dropped)
	}
	for _, idx := range redundantEdgeIndexes {
		if hasIndex(t, db, idx) {
			t.Errorf("%s 未被删除", idx)
		}
	}
	// 幂等：再跑一次无动作无错误
	again, err := dropRedundantEdgeIndexes(db)
	if err != nil || len(again) != 0 {
		t.Errorf("迁移应幂等：dropped=%v err=%v", again, err)
	}
	// 数据未受影响
	nodes, edges, err := repo.Counts()
	if err != nil || nodes != 2 || edges != 1 {
		t.Errorf("迁移后数据异常：nodes=%d edges=%d err=%v", nodes, edges, err)
	}
}

// Q254：关闭外键构建时插入的悬挂边，末尾一次性清理并按条数报告。
func TestDropDanglingEdges(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	repo := NewRepo(db)
	save(t, repo, []*domain.CodeEntity{
		{ID: "symbol:go:x:a", Kind: domain.KindFunction, Name: "a"},
		{ID: "symbol:go:x:b", Kind: domain.KindFunction, Name: "b"},
	}, []*domain.Fact{{SourceID: "symbol:go:x:a", TargetID: "symbol:go:x:b", Kind: domain.FactCalls}})

	if err := db.SetForeignKeys(false); err != nil {
		t.Fatal(err)
	}
	// Q254c：edges 用整数代理键——"悬挂边"= 引用不存在的 id_int（FK 关闭时
	// 可插入，用于验证末尾清理；正常写路径由 Go 侧解析端点，不会产生）
	var realRef int64
	if err := db.QueryRow("SELECT id_int FROM nodes WHERE id = ?", "symbol:go:x:a").Scan(&realRef); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO edges(source_ref, target_ref, kind, tool_source)
		VALUES(?, 999999, 'calls', 'ssa'), (999999, ?, 'calls', 'ssa')`, realRef, realRef); err != nil {
		t.Fatalf("FK 关闭时应能插入悬挂边：%v", err)
	}
	n, err := db.CheckNoDanglingEdges()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("应有 2 条悬挂边，实际 %d", n)
	}
	dropped, err := db.DropDanglingEdges(1000)
	if err != nil {
		t.Fatalf("DropDanglingEdges: %v", err)
	}
	if dropped != 2 {
		t.Errorf("应清理 2 条，实际 %d", dropped)
	}
	if n, _ := db.CheckNoDanglingEdges(); n != 0 {
		t.Errorf("清理后仍有 %d 条悬挂边", n)
	}
	_, edges, err := repo.Counts()
	if err != nil || edges != 1 {
		t.Errorf("合法边应保留：edges=%d err=%v", edges, err)
	}
}

// Q254：WAL 收尾——checkpoint 后 WAL 文件归零，且 journal_size_limit 生效。
func TestCheckpointTruncateShrinksWAL(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	var limit int
	if err := db.QueryRow("PRAGMA journal_size_limit").Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if limit != journalSizeLimit {
		t.Errorf("journal_size_limit=%d，want %d（Q254：WAL 文件上限）", limit, journalSizeLimit)
	}
	repo := NewRepo(db)
	for i := 0; i < 200; i++ {
		save(t, repo, []*domain.CodeEntity{{
			ID: domain.CanonicalID(fmt.Sprintf("symbol:go:x:w%03d", i)), Kind: domain.KindFunction,
			Name: fmt.Sprintf("w%03d", i), Properties: map[string]any{"pad": padStr()},
		}}, nil)
	}
	if db.walSize() == 0 {
		t.Skip("本环境 WAL 未落盘（自动化 checkpoint 已回收）——跳过断言")
	}
	if _, err := db.CheckpointTruncate(); err != nil {
		t.Fatalf("CheckpointTruncate: %v", err)
	}
	if sz := db.walSize(); sz != 0 {
		t.Errorf("TRUNCATE 后 WAL 应为 0，实际 %d 字节", sz)
	}
}

func TestSetForeignKeys(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := db.SetForeignKeys(false); err != nil {
		t.Fatal(err)
	}
	if !foreignKeysOff(t, db) {
		t.Error("关外键失败")
	}
	if err := db.SetForeignKeys(true); err != nil {
		t.Fatal(err)
	}
	if foreignKeysOff(t, db) {
		t.Error("开外键失败")
	}
}

func foreignKeysOff(t *testing.T, db *DB) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 0
}

func hasIndex(t *testing.T, db *DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func padStr() string {
	b := make([]byte, 200)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}
