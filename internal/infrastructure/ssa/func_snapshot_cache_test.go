package ssa

import (
	"go/token"
	"go/types"
	"reflect"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// testFuncSnapshotFiles 用给定 fixture 构建 funcSnapshot。
func testFuncSnapshotFiles(t *testing.T, files map[string]string) *funcSnapshot {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, dir+"/"+name, content)
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	prog, _ := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		t.Fatal("ssautil.Packages 失败")
	}
	prog.Build() // 构建函数体（否则 Blocks 为空，静态调用点扫描不到）
	return newFuncSnapshot(prog)
}

// testFuncSnapshot 用 base fixture 构建 funcSnapshot 与一个具名函数对象。
func testFuncSnapshot(t *testing.T) (*funcSnapshot, *types.Func) {
	t.Helper()
	s := testFuncSnapshotFiles(t, map[string]string{"go.mod": baseModule, "main.go": baseMain})
	var fn *types.Func
	for _, f := range s.all() {
		if obj, ok := f.Object().(*types.Func); ok && obj != nil {
			fn = obj
			break
		}
	}
	return s, fn
}

// TestFuncSnapshotIdentityCache：构建级函数身份缓存——结果与 funcIdentity
// 一致、重复调用命中缓存（map 只增一条）、nil 返回空。
func TestFuncSnapshotIdentityCache(t *testing.T) {
	s, fn := testFuncSnapshot(t)
	if fn == nil {
		t.Fatal("fixture 未找到具名函数")
	}
	want, kind, name := funcIdentity(fn)
	got := s.identityOf(fn)
	if got.id != want || got.kind != kind || got.name != name {
		t.Fatalf("identityOf = %+v; want {%s %s %s}", got, want, kind, name)
	}
	if len(s.idents) != 1 {
		t.Fatalf("首次调用应写入 1 条缓存，got %d", len(s.idents))
	}
	for i := 0; i < 5; i++ {
		_ = s.identityOf(fn)
	}
	if len(s.idents) != 1 {
		t.Fatalf("重复调用不应新增缓存，got %d", len(s.idents))
	}
	if s.identityOf(nil).id != "" {
		t.Error("nil 函数应返回空身份")
	}
}

// legacyStaticCallSites 旧实现的等价扫描（无索引：全程序遍历，顺序为
// 函数列表 → 块 → 指令）。
func legacyStaticCallSites(s *funcSnapshot) map[*ssa.Function][][]ssa.Value {
	idx := map[*ssa.Function][][]ssa.Value{}
	for _, f := range s.all() {
		for _, b := range f.Blocks {
			for _, instr := range b.Instrs {
				call, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				callee := call.Call.StaticCallee()
				if callee == nil {
					continue
				}
				idx[callee] = append(idx[callee], call.Call.Args)
			}
		}
	}
	return idx
}

// TestStaticCallSitesIndexMatchesLegacyScan：构建级静态调用点索引与旧实现
// 的全程序扫描在「扫描范围/调用形式/实参/顺序」上逐函数一致；惰性构建
// 只做一次（sync.Once）。
func TestStaticCallSitesIndexMatchesLegacyScan(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod": baseModule,
		"main.go": `package main

type Svc struct{}

func (s *Svc) Handle(v string) {}

func helper(x int) int { return x + 1 }

func uncalled() {}

func main() {
	svc := &Svc{}
	svc.Handle("a")
	svc.Handle("b")
	_ = helper(1)
	_ = helper(2)
}
`,
	})
	idx := s.staticCallSites()
	if idx == nil {
		t.Fatal("索引不应为 nil")
	}
	if again := s.staticCallSites(); reflect.ValueOf(again).UnsafePointer() != reflect.ValueOf(idx).UnsafePointer() {
		t.Error("staticCallSites 应惰性构建一次（返回同一 map）")
	}
	legacy := legacyStaticCallSites(s)
	if len(idx) != len(legacy) {
		t.Fatalf("索引条目数 %d != 旧实现 %d", len(idx), len(legacy))
	}
	for fn, want := range legacy {
		if !reflect.DeepEqual(idx[fn], want) {
			t.Errorf("调用点不一致 %s: idx=%v legacy=%v", fn.Name(), idx[fn], want)
		}
	}
	// 无调用点的函数不出现在索引里（不误建）
	for _, f := range s.all() {
		if f.Name() == "uncalled" {
			if got := idx[f]; len(got) != 0 {
				t.Errorf("无调用点的函数不应有条目，got %v", got)
			}
		}
	}
}

// TestStaticCallSitesRecursive：递归调用会同时出现「自身调用点 + 外部
// 调用点」，索引不丢。
func TestStaticCallSitesRecursive(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod": baseModule,
		"main.go": `package main

func rec(n int) int {
	if n <= 0 {
		return 0
	}
	return rec(n-1)
}

func main() { _ = rec(3) }
`,
	})
	idx := s.staticCallSites()
	var rec *ssa.Function
	for _, f := range s.all() {
		if f.Name() == "rec" {
			rec = f
			break
		}
	}
	if rec == nil {
		t.Fatal("未找到 rec")
	}
	if got := len(idx[rec]); got != 2 {
		t.Fatalf("递归函数应有 2 个调用点（自调用 + main），got %d", got)
	}
}

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
