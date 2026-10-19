package ssa

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestIndirectWriteMultiFieldSameCall（todo8-10/11）：同一调用点传播多个
// 字段时——直接写切片与间接写映射都参与匹配；实参名只拼接一次（各字段
// 的调用点元数据一致）。
func TestIndirectWriteMultiFieldSameCall(t *testing.T) {
	nodes, facts, summaries := indexFixtureFull(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Order struct {
	A int
	B int
}

func fill(o *Order) {
	o.A = 1
	o.B = 2
}

func use(o *Order) { fill(o) }
`,
	})
	_ = nodes
	// 两个字段的间接写摘要
	want := map[string]bool{"example.com/mtest.Order.A": false, "example.com/mtest.Order.B": false}
	for _, s := range summaries {
		if s.AccessKind == domain.SummaryIndirectWrite {
			if _, ok := want[s.FieldPath]; ok {
				want[s.FieldPath] = true
			}
		}
	}
	for f, ok := range want {
		if !ok {
			t.Errorf("缺少间接写摘要 %s", f)
		}
	}
	// INDIRECT_WRITE 边：call_args 为实参变量名（同一调用点只拼一次）
	found := false
	for _, f := range facts {
		if f.Kind != domain.FactIndirectWrite {
			continue
		}
		found = true
		if v, _ := f.Metadata["call_args"].(string); v != "o" {
			t.Errorf("call_args = %v, want o", f.Metadata["call_args"])
		}
	}
	if !found {
		t.Error("未产出 INDIRECT_WRITE 边")
	}
}
