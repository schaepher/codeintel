package scip

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func testItems() []artifactItem {
	return []artifactItem{
		{Node: &domain.CodeEntity{ID: "symbol:go:example.com/a:F", Kind: domain.KindFunction,
			Name: "F", FilePath: "a.go", LineStart: 3,
			Properties: map[string]any{"signature": "func F()", "line": 3}}},
		{Fact: &domain.Fact{SourceID: "symbol:go:example.com/a:F",
			TargetID: "symbol:go:example.com/a:I", Kind: domain.FactImplements,
			Confidence: 1.0, ToolSource: "scip", Metadata: map[string]any{"line": 7}}},
		{Node: &domain.CodeEntity{ID: "symbol:go:example.com/a:I", Kind: domain.KindInterface,
			Name: "I", FilePath: "a.go", LineStart: 9}},
	}
}

func TestArtifactCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	items := testItems()
	// > 一块，验证跨块顺序
	for i := 0; i < artifactChunkSize*2+7; i++ {
		items = append(items, artifactItem{Node: &domain.CodeEntity{
			ID:   domain.CanonicalID("symbol:go:example.com/a:N" + itoaSmall(i)),
			Kind: domain.KindFunction, Name: "N", Properties: map[string]any{"idx": i},
		}})
	}
	key := scipCacheKey("content-key", "0.2.7", []string{".", "sub"})
	path := scipCachePath(dir)
	if err := saveArtifacts(path, key, "0.2.7", items); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := loadArtifacts(path, key, "0.2.7")
	if !ok {
		t.Fatal("load miss after save")
	}
	if len(got) != len(items) {
		t.Fatalf("len = %d, want %d", len(got), len(items))
	}
	for i := range items {
		if (got[i].Node == nil) != (items[i].Node == nil) || (got[i].Fact == nil) != (items[i].Fact == nil) {
			t.Fatalf("item %d kind mismatch", i)
		}
		if items[i].Node != nil {
			if got[i].Node.ID != items[i].Node.ID || got[i].Node.LineStart != items[i].Node.LineStart {
				t.Fatalf("item %d node mismatch: %+v vs %+v", i, got[i].Node, items[i].Node)
			}
			if len(got[i].Node.Properties) != len(items[i].Node.Properties) {
				t.Fatalf("item %d props mismatch: %v vs %v", i, got[i].Node.Properties, items[i].Node.Properties)
			}
		}
		if items[i].Fact != nil {
			if got[i].Fact.SourceID != items[i].Fact.SourceID || got[i].Fact.TargetID != items[i].Fact.TargetID ||
				got[i].Fact.Kind != items[i].Fact.Kind || got[i].Fact.Confidence != items[i].Fact.Confidence {
				t.Fatalf("item %d fact mismatch: %+v vs %+v", i, got[i].Fact, items[i].Fact)
			}
			if got[i].Fact.Metadata["line"] != float64(7) && got[i].Fact.Metadata["line"] != 7 {
				t.Fatalf("item %d metadata line = %v", i, got[i].Fact.Metadata["line"])
			}
		}
	}
}

func TestArtifactCacheRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	key := scipCacheKey("content-a", "0.2.7", []string{"."})
	path := scipCachePath(dir)
	if err := saveArtifacts(path, key, "0.2.7", testItems()); err != nil {
		t.Fatalf("save: %v", err)
	}
	cases := []struct{ name, key, ver string }{
		{"key", scipCacheKey("content-b", "0.2.7", []string{"."}), "0.2.7"},
		{"module", scipCacheKey("content-a", "0.2.7", []string{".", "sub"}), "0.2.7"},
		{"version", key, "0.2.8"},
	}
	for _, c := range cases {
		if _, ok := loadArtifacts(path, c.key, c.ver); ok {
			t.Fatalf("%s mismatch must not hit", c.name)
		}
	}
	if _, ok := loadArtifacts(filepath.Join(dir, "missing.gob"), key, "0.2.7"); ok {
		t.Fatal("missing file must not hit")
	}
}

func TestArtifactCacheRejectsTruncatedAndMissingEnd(t *testing.T) {
	dir := t.TempDir()
	key := scipCacheKey("content", "0.2.7", []string{"."})
	path := scipCachePath(dir)
	if err := saveArtifacts(path, key, "0.2.7", testItems()); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 截断（去掉结束标记所在尾部）
	if err := os.WriteFile(path, raw[:len(raw)-8], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadArtifacts(path, key, "0.2.7"); ok {
		t.Fatal("truncated cache must not hit")
	}
	// 头部完好、载荷前中断
	if err := os.WriteFile(path, raw[:20], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadArtifacts(path, key, "0.2.7"); ok {
		t.Fatal("partial cache must not hit")
	}
	// 结束标记带载荷：手工写一个 End=true 且带 Items 的块
	nopath := scipCachePath(dir)
	if err := writeEndWithPayload(nopath, key); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadArtifacts(nopath, key, "0.2.7"); ok {
		t.Fatal("end marker with payload must not hit")
	}
}

func TestIndexArgs(t *testing.T) {
	base := indexArgs("/tmp/i.scip", nil)
	want := []string{"index", "-o", "/tmp/i.scip", "-q", "--skip-tests"}
	if len(base) != len(want) {
		t.Fatalf("args = %v, want %v", base, want)
	}
	for i := range want {
		if base[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, base[i], want[i])
		}
	}
	withPats := indexArgs("/tmp/i.scip", []string{"./internal/impl/passport", "./internal/impl/service"})
	if len(withPats) != len(want)+2 {
		t.Fatalf("pattern args = %v", withPats)
	}
	if withPats[5] != "./internal/impl/passport" || withPats[6] != "./internal/impl/service" {
		t.Fatalf("patterns not appended: %v", withPats)
	}
}

func TestScipCacheKeySensitivity(t *testing.T) {
	a := scipCacheKey("c1", "0.2.7", []string{"."})
	b := scipCacheKey("c2", "0.2.7", []string{"."})
	c := scipCacheKey("c1", "0.2.8", []string{"."})
	d := scipCacheKey("c1", "0.2.7", []string{".", "x"})
	if a == b || a == c || a == d {
		t.Fatalf("cache key must change with content/version/modules: %s %s %s %s", a, b, c, d)
	}
	if a != scipCacheKey("c1", "0.2.7", []string{"."}) {
		t.Fatal("cache key must be stable")
	}
}

// itoaSmall 测试用十进制整数转字符串（避免引入额外依赖）。
func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// writeEndWithPayload 写一份"结束标记携带载荷"的非法缓存（验证拒绝路径）。
func writeEndWithPayload(path, key string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := gob.NewEncoder(f)
	if err := enc.Encode(artifactHeader{Version: scipCacheFormat, Format: "scip-artifacts",
		Key: key, ScipVersion: "0.2.7"}); err != nil {
		return err
	}
	return enc.Encode(artifactChunk{Items: testItems(), End: true})
}
