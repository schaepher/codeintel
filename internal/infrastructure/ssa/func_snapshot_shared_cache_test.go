package ssa

// 构建级共享缓存测试（Return 操作数 / 类型表名 / SQL 解析）——从
// func_snapshot_cache_test.go 拆出（行数治理）。

import (
	"go/token"
	"go/types"
	"reflect"
	"testing"

	"golang.org/x/tools/go/ssa"
)

// TestFuncSnapshotReturnOperandsCache：构建级 Return 操作数缓存——结果与
// returnOperands 一致、重复调用命中单条缓存、nil 返回 nil。
func TestFuncSnapshotReturnOperandsCache(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod": baseModule,
		"main.go": `package main

func pair() (int, string) { return 1, "x" }

func none() {}

func main() { _, _ = pair(); none() }
`,
	})
	var pair, none *ssa.Function
	for _, f := range s.all() {
		switch f.Name() {
		case "pair":
			pair = f
		case "none":
			none = f
		}
	}
	if pair == nil || none == nil {
		t.Fatal("fixture 未找到函数")
	}
	want := returnOperands(pair)
	got := s.returnOperandsOf(pair)
	if !reflect.DeepEqual(got, want) || len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("returnOperandsOf = %v; want %v", got, want)
	}
	if len(s.rets) != 1 {
		t.Fatalf("首次调用应写入 1 条缓存，got %d", len(s.rets))
	}
	for i := 0; i < 5; i++ {
		_ = s.returnOperandsOf(pair)
	}
	if len(s.rets) != 1 {
		t.Fatalf("重复调用不应新增缓存，got %d", len(s.rets))
	}
	// 无返回函数也缓存（Return 指令存在但 Results 为空）
	if got, want := s.returnOperandsOf(none), returnOperands(none); !reflect.DeepEqual(got, want) {
		t.Errorf("无返回函数 = %v; want %v", got, want)
	}
	if len(s.rets) != 2 {
		t.Errorf("空结果也应缓存，got %d", len(s.rets))
	}
	if s.returnOperandsOf(nil) != nil {
		t.Error("nil 函数应返回 nil")
	}
}

// TestFuncSnapshotTableNameCache：构建级表名缓存——compute 只调用一次
// （重复查询命中）、空结果也缓存、nil 退化直接 compute。
func TestFuncSnapshotTableNameCache(t *testing.T) {
	s, _ := testFuncSnapshot(t)
	mk := func(name string) *types.Named {
		pkg := types.NewPackage("example.com/x", "x")
		obj := types.NewTypeName(token.NoPos, pkg, name, nil)
		return types.NewNamed(obj, types.NewStruct(nil, nil), nil)
	}
	n1 := mk("T1")
	calls := 0
	compute := func() string { calls++; return "tb1" }
	if got := s.tableNameCached(n1, compute); got != "tb1" {
		t.Fatalf("表名 = %q; want tb1", got)
	}
	for i := 0; i < 5; i++ {
		if got := s.tableNameCached(n1, compute); got != "tb1" {
			t.Fatalf("命中返回 %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("compute 应只调用一次，got %d", calls)
	}
	// 空结果也缓存
	n2 := mk("T2")
	emptyCalls := 0
	if got := s.tableNameCached(n2, func() string { emptyCalls++; return "" }); got != "" {
		t.Fatalf("空表名 = %q", got)
	}
	_ = s.tableNameCached(n2, func() string { emptyCalls++; return "" })
	if emptyCalls != 1 {
		t.Fatalf("空结果应缓存，compute 调用 %d 次", emptyCalls)
	}
}

// TestParseSQLStmtCachedStats：SQL 解析缓存——命中复用结果，且降级统计
// 保持「按调用」口径（AST 成功/失败各按调用次数计）；VACUUM 等短路命令
// 不占降级计数。
func TestParseSQLStmtCachedStats(t *testing.T) {
	s, _ := testFuncSnapshot(t)
	ext := &fieldExtractor{funcs: s}
	ResetSQLStats()
	defer ResetSQLStats()

	const astSQL = "SELECT id, name FROM user_tab WHERE id = ?"
	ext.parseSQLStmtCached(astSQL)
	ext.parseSQLStmtCached(astSQL) // 命中
	if _, ok := s.sqlParseCached(astSQL); !ok {
		t.Fatal("首次解析应写缓存")
	}
	if got := sqlAstOK.Load(); got != 2 {
		t.Errorf("AST 成功应按调用计 2 次，got %d", got)
	}

	const badSQL = "SELECT * FROM %s WHERE id = ?"
	ext.parseSQLStmtCached(badSQL)
	ext.parseSQLStmtCached(badSQL) // 命中
	if got := sqlAstFail.Load(); got != 2 {
		t.Errorf("AST 失败应按调用计 2 次，got %d", got)
	}

	before := sqlAstOK.Load() + sqlAstFail.Load()
	ext.parseSQLStmtCached("VACUUM")
	ext.parseSQLStmtCached("VACUUM")
	if after := sqlAstOK.Load() + sqlAstFail.Load(); after != before {
		t.Errorf("VACUUM 不应占降级计数（%d → %d）", before, after)
	}
}

// TestExtractorIdentityFallsBackWithoutSnapshot：ext.funcs 缺失（如
// emitGlobalInit 的轻量提取器）时 identityOf 回退纯 funcIdentity，不静默
// 返回空 ID（曾因此丢 50 个 go2o global-init ssa_value 节点）。
func TestExtractorIdentityFallsBackWithoutSnapshot(t *testing.T) {
	_, fn := testFuncSnapshot(t)
	if fn == nil {
		t.Fatal("fixture 未找到具名函数")
	}
	ext := &fieldExtractor{} // funcs == nil
	want, kind, name := funcIdentity(fn)
	got := ext.identityOf(fn)
	if got.id != want || got.kind != kind || got.name != name {
		t.Fatalf("无快照回退 = %+v; want {%s %s %s}", got, kind, name, want)
	}
}
