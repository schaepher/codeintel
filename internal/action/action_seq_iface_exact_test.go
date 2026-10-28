package action

// todo22 §7.1 测试：接口派发只走**精确**查询——拒绝模糊符号扫描与全量
// implements 扫描；固定输入下给出冷暖查询耗时与查询次数对照。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// strictReader 包一层 Reader：记录并拒绝"模糊名称搜索/全图扫描"类调用。
type strictReader struct {
	Reader
	t *testing.T

	implementsAll   int // GetImplementsEdges（全量 implements 边）
	fuzzyNameLookup int // GetSymbolByName（模糊名称扫描）
	allNames        int // AllSymbolNames（全名池）
	expand          int // Expand（整图展开）
	allNodeIDs      int // AllNodeIDs（全节点）
	exactImpls      int // InterfaceImplsExact（允许的精确查询）
}

func (s *strictReader) GetImplementsEdges() ([]*domain.Fact, error) {
	s.implementsAll++
	return nil, errors.New("禁止：一次派发不得加载全量 implements 边（§7.1）")
}

func (s *strictReader) GetSymbolByName(name string) ([]*domain.CodeEntity, error) {
	s.fuzzyNameLookup++
	return nil, errors.New("禁止：内部 canonical ID 不得走模糊名称搜索（§7.1）")
}

func (s *strictReader) AllSymbolNames(limit int) ([]string, error) {
	s.allNames++
	return nil, errors.New("禁止：不得拉全名池（§7.1）")
}

func (s *strictReader) Expand(id domain.CanonicalID) ([]*domain.Fact, []*domain.CodeEntity, error) {
	s.expand++
	return nil, nil, errors.New("禁止：一次派发不得 Expand 整图（§7.1）")
}

func (s *strictReader) AllNodeIDs() ([]domain.CanonicalID, error) {
	s.allNodeIDs++
	return nil, errors.New("禁止：不得加载全节点（§7.1）")
}

func (s *strictReader) InterfaceImplsExact(ifaceID string) ([]string, error) {
	s.exactImpls++
	return s.Reader.InterfaceImplsExact(ifaceID)
}

// TestSeqIfaceExactQueriesAndBudget：接口派发只用精确查询；冷暖查询次数一致。
func TestSeqIfaceExactQueriesAndBudget(t *testing.T) {
	src := `package m

type Doer interface{ Do() }

type A struct{}
type B struct{}

func (a *A) Do() { OnlyA() }
func (b *B) Do() { OnlyB() }

func OnlyA() {}
func OnlyB() {}

func Run(d Doer) { d.Do() }
`
	acts, dir := seedIfaceRepo(t, src, []string{"A", "B"})
	strict := &strictReader{Reader: acts.repo, t: t}
	acts.repo = strict

	// 入口用 canonical ID（用户输入的模糊解析属另一条已测路径 §14.1；这里
	// 专门验证"内部派发不得模糊/全扫"）
	req := CodeSequenceRequest{Target: "symbol:go:example.com/m:Run", RepoAbs: dir, Depth: 3}
	root, err := acts.CodeSequence(req)
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	if root.Meta == nil || root.Meta.Timing == nil {
		t.Fatalf("缺少 timing 元信息")
	}
	coldQueries := root.Meta.Timing.SQLQueries
	coldMS := root.Meta.Timing.TotalMS

	// 同一输入第二次（暖）：文件解析/解析缓存命中，查询次数必须一致
	root2, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Run", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence(warm): %v", err)
	}
	warmQueries := root2.Meta.Timing.SQLQueries
	warmMS := root2.Meta.Timing.TotalMS

	if strict.implementsAll != 0 {
		t.Errorf("不得加载全量 implements 边（调用 %d 次）", strict.implementsAll)
	}
	if strict.fuzzyNameLookup != 0 || strict.allNames != 0 {
		t.Errorf("不得走模糊名称搜索/全名池：GetSymbolByName=%d AllSymbolNames=%d",
			strict.fuzzyNameLookup, strict.allNames)
	}
	if strict.expand != 0 || strict.allNodeIDs != 0 {
		t.Errorf("不得整图展开：Expand=%d AllNodeIDs=%d", strict.expand, strict.allNodeIDs)
	}
	if strict.exactImpls == 0 {
		t.Errorf("应使用精确接口查询 InterfaceImplsExact（0 次）")
	}
	if coldQueries != warmQueries {
		t.Errorf("冷暖查询次数应一致（查询次数是确定性证据）：cold=%d warm=%d", coldQueries, warmQueries)
	}
	// 固定输入下的查询预算（超出说明有隐藏全扫/重复查询）
	const budget = 40
	if coldQueries > budget {
		t.Errorf("单次派发查询次数 %d 超过预算 %d", coldQueries, budget)
	}
	t.Logf("[seq.iface] cold: queries=%d total=%.3fms", coldQueries, coldMS)
	t.Logf("[seq.iface] warm: queries=%d total=%.3fms", warmQueries, warmMS)
}

// TestSeqIfaceImplCachePerRequest：同一请求里同一接口的多处派发只查一次
// （todo22 §7.3：请求内缓存，命中与未命中都缓存）。
func TestSeqIfaceImplCachePerRequest(t *testing.T) {
	src := `package m

type Doer interface{ Do() }

type A struct{}
type B struct{}

func (a *A) Do() { OnlyA() }
func (b *B) Do() { OnlyB() }

func OnlyA() {}
func OnlyB() {}

func Run(d Doer) {
	d.Do()
	d.Do()
}
`
	acts, dir := seedIfaceRepo(t, src, []string{"A", "B"})
	strict := &strictReader{Reader: acts.repo, t: t}
	acts.repo = strict
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Run", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	if got := len(seqCallNodesNamed(root, "d.Do")); got != 2 {
		t.Fatalf("应有 2 个 d.Do 调用点，got %d", got)
	}
	if strict.exactImpls != 1 {
		t.Errorf("同一请求里同一接口应只查一次实现候选，got %d", strict.exactImpls)
	}
	if strict.implementsAll != 0 {
		t.Errorf("不得加载全量 implements 边，got %d", strict.implementsAll)
	}
	// 缓存不影响结论：两个调用点都必须保留未消歧候选
	for _, n := range seqCallNodesNamed(root, "d.Do") {
		if n.Evidence == nil || len(n.Evidence.Candidates) != 2 || !n.Evidence.Ambiguous {
			t.Errorf("缓存命中后仍应保留 2 个未消歧候选，got %+v", n.Evidence)
		}
	}
}

// TestSeqIfaceCacheMethodIdentity：同一接口的多个方法不能复用同一份
// chosen/candidates（todo23 §7.1）——缓存实现类型、按当前方法重建 ID。
func TestSeqIfaceCacheMethodIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:API", Kind: domain.KindInterface, Name: "API"},
		{ID: "symbol:go:example.com/m:(API).Insert", Kind: domain.KindMethod, Name: "(API).Insert"},
		{ID: "symbol:go:example.com/m:(API).SaveService", Kind: domain.KindMethod, Name: "(API).SaveService"},
		{ID: "symbol:go:example.com/m:Store", Kind: domain.KindStruct, Name: "Store"},
		{ID: "symbol:go:example.com/m:(Store).Insert", Kind: domain.KindMethod, Name: "(Store).Insert"},
		{ID: "symbol:go:example.com/m:(Store).SaveService", Kind: domain.KindMethod, Name: "(Store).SaveService"},
	}, []*domain.Fact{{
		SourceID: "symbol:go:example.com/m:API", TargetID: "symbol:go:example.com/m:Store",
		Kind: domain.FactImplements, Confidence: 1.0,
	}}, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	req := CodeSequenceRequest{ifaceImpls: map[string]seqIfaceCached{},
		ifaceImplTypes: map[string][]string{}}

	chosen1, cands1, ok1 := acts.seqIfaceMethodImpl(req, "symbol:go:example.com/m:(API).Insert")
	if !ok1 || chosen1 != "symbol:go:example.com/m:(Store).Insert" {
		t.Fatalf("Insert 应具体化到 (Store).Insert，got %q (%v)", chosen1, ok1)
	}
	chosen2, cands2, ok2 := acts.seqIfaceMethodImpl(req, "symbol:go:example.com/m:(API).SaveService")
	if !ok2 || chosen2 != "symbol:go:example.com/m:(Store).SaveService" {
		t.Fatalf("SaveService 应具体化到 (Store).SaveService，got %q (%v)", chosen2, ok2)
	}
	for _, c := range cands2 {
		if strings.HasSuffix(c, ").Insert") {
			t.Errorf("SaveService 的候选不得复用 Insert 的 ID：%v", cands2)
		}
	}
	if len(cands1) != 1 || !strings.HasSuffix(cands1[0], ").Insert") {
		t.Errorf("Insert 候选应为 (Store).Insert，got %v", cands1)
	}
	missing, candsM, okM := acts.seqIfaceMethodImpl(req, "symbol:go:example.com/m:(API).Missing")
	if okM || missing != "" || len(candsM) != 0 {
		t.Errorf("Missing 不应借用其它方法的结果，got %q %v (%v)", missing, candsM, okM)
	}
	// 实现类型查询只一次（Insert/SaveService 共用）；方法身份各自缓存
	if len(req.ifaceImplTypes["symbol:go:example.com/m:API"]) != 1 {
		t.Errorf("实现类型缓存应有 1 个实现，got %v", req.ifaceImplTypes)
	}
}

// seedIfaceRepoWithImpl 建一个最小接口仓库：API 的 Insert/SaveService 由
// implType 实现（两个方法都存在，用于验证跨仓同名接口不串候选）。
func seedIfaceRepoWithImpl(t *testing.T, implType string) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/api\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	base := "symbol:go:example.com/api:"
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: domain.CanonicalID(base + "API"), Kind: domain.KindInterface, Name: "API"},
		{ID: domain.CanonicalID(base + "(API).Insert"), Kind: domain.KindMethod, Name: "(API).Insert"},
		{ID: domain.CanonicalID(base + "(API).SaveService"), Kind: domain.KindMethod, Name: "(API).SaveService"},
		{ID: domain.CanonicalID(base + implType), Kind: domain.KindStruct, Name: implType},
		{ID: domain.CanonicalID(base + "(" + implType + ").Insert"), Kind: domain.KindMethod, Name: "(" + implType + ").Insert"},
		{ID: domain.CanonicalID(base + "(" + implType + ").SaveService"), Kind: domain.KindMethod, Name: "(" + implType + ").SaveService"},
	}, []*domain.Fact{{
		SourceID: domain.CanonicalID(base + "API"), TargetID: domain.CanonicalID(base + implType),
		Kind: domain.FactImplements, Confidence: 1.0,
	}}, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestSeqIfaceCacheRepoIsolation：外层 workspace 请求复用同一份请求缓存时，
// 同名接口（相同 canonical ID）在两个仓库的实现候选不得互相串用；Missing
// 也不得借用 Insert 的结果。
func TestSeqIfaceCacheRepoIsolation(t *testing.T) {
	actsA, dirA := seedIfaceRepoWithImpl(t, "StoreA")
	actsB, dirB := seedIfaceRepoWithImpl(t, "StoreB")
	req := CodeSequenceRequest{RepoAbs: dirA, ifaceImpls: map[string]seqIfaceCached{},
		ifaceImplTypes: map[string][]string{}}

	insertA, candsA, ok := actsA.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).Insert")
	if !ok || insertA != "symbol:go:example.com/api:(StoreA).Insert" {
		t.Fatalf("仓库 A 的 Insert 应具体化到 StoreA.Insert，got %q %v", insertA, candsA)
	}
	if len(candsA) != 1 || !strings.HasSuffix(candsA[0], "(StoreA).Insert") {
		t.Errorf("仓库 A 候选不得含其它仓库实现：%v", candsA)
	}
	saveA, candsAS, ok := actsA.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).SaveService")
	if !ok || saveA != "symbol:go:example.com/api:(StoreA).SaveService" {
		t.Errorf("仓库 A 的 SaveService 应具体化到 StoreA.SaveService，got %q %v", saveA, candsAS)
	}

	req.RepoAbs = dirB
	insertB, candsB, ok := actsB.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).Insert")
	if !ok || insertB != "symbol:go:example.com/api:(StoreB).Insert" {
		t.Fatalf("仓库 B 的 Insert 应具体化到 StoreB.Insert（不得命中 A 的缓存），got %q %v", insertB, candsB)
	}
	if len(candsB) != 1 || !strings.HasSuffix(candsB[0], "(StoreB).Insert") {
		t.Errorf("仓库 B 候选不得含 A 的实现：%v", candsB)
	}
	missing, candsM, okM := actsB.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).Missing")
	if okM || missing != "" || len(candsM) != 0 {
		t.Errorf("Missing 不得借用 Insert/SaveService 或其它仓库的结果，got %q %v (%v)", missing, candsM, okM)
	}

	// 冷暖对照：同一仓库第二次查询（缓存命中）结论逐项相同。
	again, candsAgain, okAgain := actsB.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).Insert")
	if !okAgain || again != insertB || strings.Join(candsAgain, "|") != strings.Join(candsB, "|") {
		t.Errorf("暖查询结论应与冷查询一致：cold=%q %v warm=%q %v", insertB, candsB, again, candsAgain)
	}
}
