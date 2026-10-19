package ssa

import (
	"sync"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestMergeFuncDataOrderAndEmpty（todo9-5）：多份贡献按到达顺序追加；
// 空贡献不建立 owner 条目（与预分配改动前语义一致）。
func TestMergeFuncDataOrderAndEmpty(t *testing.T) {
	var mu sync.Mutex
	data := map[domain.CanonicalID]*funcData{}
	first := &funcData{
		directReads:    []fieldEntry{{fieldPath: "r1"}},
		directWrites:   []fieldEntry{{fieldPath: "w1"}},
		calls:          []callInfo{{calleeID: "c1"}},
		indirectWrites: []fieldEntry{{fieldPath: "i1"}},
	}
	second := &funcData{
		directReads:  []fieldEntry{{fieldPath: "r2"}},
		directWrites: []fieldEntry{{fieldPath: "w2"}},
		calls:        []callInfo{{calleeID: "c2"}},
	}
	mergeFuncData(&mu, data, "f", first)
	mergeFuncData(&mu, data, "f", second)
	d := data["f"]
	if d == nil || len(d.directReads) != 2 || d.directReads[0].fieldPath != "r1" || d.directReads[1].fieldPath != "r2" {
		t.Fatalf("directReads 合并顺序错误: %+v", d)
	}
	if len(d.directWrites) != 2 || d.directWrites[1].fieldPath != "w2" {
		t.Fatalf("directWrites 合并顺序错误: %+v", d)
	}
	if len(d.calls) != 2 || d.calls[1].calleeID != "c2" {
		t.Fatalf("calls 合并顺序错误: %+v", d)
	}
	if len(d.indirectWrites) != 1 || d.indirectWrites[0].fieldPath != "i1" {
		t.Fatalf("indirectWrites 合并错误: %+v", d)
	}
	mergeFuncData(&mu, data, "g", &funcData{})
	if _, ok := data["g"]; ok {
		t.Fatal("空贡献不应建立 owner 条目")
	}
	mergeFuncData(&mu, data, "", first)
	mergeFuncData(&mu, data, "nil", nil)
	if len(data) != 1 {
		t.Fatalf("空 owner/nil fd 不应写入: %d", len(data))
	}
}
