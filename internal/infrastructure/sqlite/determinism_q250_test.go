package sqlite

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q250 F2：共享节点的 `func_id` 多写者（external_summary / 外部函数节点被
// 多个调用者写入）——冲突解决必须与写入顺序无关（取字典序最小），否则
// 同一次构建两次运行的图内容不同（go2o 实测残余 47 行差异）。
func TestNodeFuncIDDeterministicAcrossWriteOrder(t *testing.T) {
	node := func(funcID string) *domain.CodeEntity {
		return &domain.CodeEntity{
			ID: "symbol:go:encoding/json:Unmarshal", Kind: domain.KindExternalSummary, Name: "Unmarshal",
			Properties: map[string]any{"func_id": funcID, "summary_json": `{"reads":""}`},
		}
	}
	// 顺序 1：先 z 后 a；顺序 2：先 a 后 z（另一条连接/另一个库）
	save := func(order []string, dbPath string) string {
		t.Helper()
		d2, err := Open(dbPath)
		if err != nil {
			t.Fatalf("open %s: %v", dbPath, err)
		}
		defer d2.Close()
		rr := NewRepo(d2)
		for _, fid := range order {
			if _, err := rr.SaveBatchStats([]*domain.CodeEntity{node(fid)}, nil, nil); err != nil {
				t.Fatalf("save %s: %v", fid, err)
			}
		}
		var out string
		if err := d2.QueryRow(`SELECT json_extract(properties,'$.func_id') FROM nodes WHERE id = ?`,
			"symbol:go:encoding/json:Unmarshal").Scan(&out); err != nil {
			t.Fatalf("read func_id: %v", err)
		}
		return out
	}
	dirA := t.TempDir()
	dirB := t.TempDir()
	got1 := save([]string{"symbol:go:m:zzz", "symbol:go:m:aaa"}, dirA)
	got2 := save([]string{"symbol:go:m:aaa", "symbol:go:m:zzz"}, dirB)
	if got1 != got2 {
		t.Fatalf("写入顺序不同导致 func_id 不同：%q vs %q", got1, got2)
	}
	if got1 != "symbol:go:m:aaa" {
		t.Errorf("应取字典序最小，got %q", got1)
	}
	// 其他属性仍按最后一次写入合并（json_patch 语义不变）
	d3, err := Open(dirA)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d3.Close()
	var summary string
	if err := d3.QueryRow(`SELECT json_extract(properties,'$.summary_json') FROM nodes LIMIT 1`).Scan(&summary); err != nil {
		t.Fatalf("summary_json 应保留：%v", err)
	}
	if summary == "" {
		t.Error("summary_json 不应丢失（json_patch 合并语义保留）")
	}
}

// Q250 F2b：共享节点（同 id 被不同函数写 func_id）也走同一规则。
// P1-11 起函数内节点（ssa_value/parameter/field_access）的 func_id 必须是
// ID 前缀（前缀范围查依赖），故这里用 external_summary——它的 func_id 是
// "算出摘要的调用方"（本就可以不是 ID 前缀），多写者合并规则仍适用。
func TestNodeFuncIDMinAlsoForExternalSummary(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := NewRepo(db)
	for _, fid := range []string{"symbol:go:m:f1", "symbol:go:m:f2"} {
		if _, err := r.SaveBatchStats([]*domain.CodeEntity{{
			ID: "symbol:go:encoding/json:Marshal", Kind: domain.KindExternalSummary, Name: "Marshal",
			Properties: map[string]any{"func_id": fid, "type_string": "int"},
		}}, nil, nil); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	var fid string
	if err := db.QueryRow(`SELECT json_extract(properties,'$.func_id') FROM nodes WHERE id='symbol:go:encoding/json:Marshal'`).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	if fid != "symbol:go:m:f1" {
		t.Errorf("func_id 应取最小，got %q", fid)
	}
}

// TestFuncIDPrefixInvariantEnforced：P1-11 写入守卫——函数内节点的
// func_id 不是 ID 前缀时 fail-closed（否则前缀范围查会静默漏节点）；
// 合规形态与其它 kind 不受影响。
func TestFuncIDPrefixInvariantEnforced(t *testing.T) {
	r := newTestRepo(t)
	bad := []*domain.CodeEntity{{
		ID: "symbol:go:m:f#t0", Kind: domain.KindSSAValue, Name: "t0",
		Properties: map[string]any{"func_id": "symbol:go:m:other"},
	}}
	if _, err := r.SaveBatchStats(bad, nil, nil); err == nil {
		t.Fatal("函数内节点 func_id 非 ID 前缀时应报错（fail-closed）")
	}
	ok := []*domain.CodeEntity{{
		ID: "symbol:go:m:f#t1", Kind: domain.KindSSAValue, Name: "t1",
		Properties: map[string]any{"func_id": "symbol:go:m:f"},
	}}
	if _, err := r.SaveBatchStats(ok, nil, nil); err != nil {
		t.Fatalf("合规前缀不应报错：%v", err)
	}
	// external_summary 的 func_id 是调用方（非前缀）——合法
	ext := []*domain.CodeEntity{{
		ID: "symbol:go:encoding/json:Unmarshal", Kind: domain.KindExternalSummary, Name: "Unmarshal",
		Properties: map[string]any{"func_id": "symbol:go:m:caller"},
	}}
	if _, err := r.SaveBatchStats(ext, nil, nil); err != nil {
		t.Fatalf("external_summary 不应套用前缀不变量：%v", err)
	}
}
