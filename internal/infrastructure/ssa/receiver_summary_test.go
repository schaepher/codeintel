package ssa

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestReceiverSummaryDirectVsInterface：同一写方法直接调用（静态 spec）
// 与经接口调用（iface spec）应产生**一致**的表.列 write 节点——接收者
// 摘要的实体/表名解析两条路径必须等价。
//
// **下标约定**（go/ssa CallCommon.Args）：静态方法调用的 Args **含
// receiver**（Args[0]=receiver），接口 invoke 的 Args **不含** receiver
// （receiver 在 cc.Value）。故静态 spec 的 ObjArg/WhereArg/IDArg 需 +1
// （见 summary_xorm.go），接口 spec 用声明参数下标（见 xorm Interface
// 条目）。本测试对同一语义的两种调用分别写对应下标，验证结果一致。
func TestReceiverSummaryDirectVsInterface(t *testing.T) {
	nodes, _, _ := indexFixtureFull(t, map[string]string{
		"go.mod": moduleGoMod,
		"field-summary.yaml": `summaries:
  - func: example.com/mtest.(repoImpl).Save
    kind: write
    obj_arg: 1
  - iface: example.com/mtest.Repo
    method: Save
    kind: write
    obj_arg: 0
`,
		"main.go": `package m

type Order struct {
	Id       int
	FinalFee float64
}

func (o *Order) TableName() string { return "mm_order" }

type Repo interface{ Save(v *Order) error }

type repoImpl struct{}

func (r *repoImpl) Save(v *Order) error { return nil }

func direct(r *repoImpl, o *Order) { _ = r.Save(o) }

func viainterface(r Repo, o *Order) { _ = r.Save(o) }
`,
	})
	writeCols := func(funcID string) map[string]bool {
		out := map[string]bool{}
		for _, n := range nodes {
			if n.Kind == domain.KindFieldAccess && n.Property("func_id") == funcID &&
				n.Property("access_kind") == "write" && n.Property("type_string") == "gorm" {
				out[n.Name] = true
			}
		}
		return out
	}
	direct := writeCols("symbol:go:example.com/mtest:direct")
	iface := writeCols("symbol:go:example.com/mtest:viainterface")
	if len(direct) == 0 {
		t.Fatalf("直调未生成 write 节点（recevier 摘要未命中）: %v", direct)
	}
	if len(iface) == 0 {
		t.Fatalf("接口调用未生成 write 节点（接收者摘要缺失）: %v", iface)
	}
	for col := range direct {
		if !iface[col] {
			t.Errorf("接口调用缺少直调有的列 %s（direct=%v interface=%v）", col, direct, iface)
		}
	}
	for col := range iface {
		if !direct[col] {
			t.Errorf("直调缺少接口调用有的列 %s（direct=%v interface=%v）", col, direct, iface)
		}
	}
}
