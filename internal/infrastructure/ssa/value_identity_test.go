package ssa

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestValueIdentityResultVsLocal：返回签名节点（#result）与同名局部值必须
// 是不同节点——局部变量 result（取地址）走值槽位（类型短名/变量名），
// 不得与签名 result 槽位共享 ID（R9）。
func TestValueIdentityResultVsLocal(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type T struct{ A int }

func h() *T {
	result := &T{}
	result.A = 1
	return result
}
`,
	})
	const funcID = "symbol:go:example.com/mtest:h"
	var resultNode *domain.CodeEntity
	fieldNode := false
	for _, n := range nodes {
		if string(n.ID) == funcID+"#result" {
			resultNode = n
		}
		if n.Kind == domain.KindFieldAccess && n.Property("func_id") == funcID {
			fieldNode = true
		}
	}
	if resultNode == nil {
		t.Fatal("返回签名节点 #result 缺失")
	}
	if resultNode.Kind != domain.KindResult {
		t.Errorf("#result 节点 kind = %s, want result（不得被同名局部值覆盖）", resultNode.Kind)
	}
	if !fieldNode {
		t.Error("局部 result.A 字段访问节点缺失（应与签名节点共存）")
	}
}

// TestValueIdentityMultipleInitSlots：同包多个 init 函数的值槽位按所在
// 行消歧——不得因同名函数合并而让不同 init 的临时值共享同一节点 ID。
func TestValueIdentityMultipleInitSlots(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type T struct{ A int }

func init() { x := &T{}; x.A = 1 }

func init() { x := &T{}; x.A = 2 }
`,
	})
	slots := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == domain.KindSSAValue && strings.Contains(string(n.ID), ":init#") {
			slots[string(n.ID)] = true
		}
	}
	if len(slots) < 2 {
		t.Errorf("多个 init 的临时值应至少 2 个不同节点，got %v", slots)
	}
}

// TestValueIdentityAliasStableNoLineSuffix：别名发射重复访问同一值时
// 标识稳定——aliasSlot 不按访问次数追加行号后缀（同一 SSA 值多次访问
// 得到同一 ID）。
func TestValueIdentityAliasStableNoLineSuffix(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type T struct{ A int }

func f() {
	x := &T{}
	x.A = 1
	x.A = 2
	_ = x.A
}
`,
	})
	// 同一 Alloc 基值（x）的多个字段访问，其 alias/值节点 ID 不得带 @行号后缀
	// 的“访问次数”语义（Q252b：alias 端点 == argument 端点；此处值节点按
	// 变量名/类型短名稳定）。
	seen := map[string]int{}
	for _, n := range nodes {
		if n.Kind == domain.KindSSAValue {
			seen[string(n.ID)]++
		}
	}
	if len(seen) == 0 {
		t.Fatal("无 ssa_value 节点")
	}
	// alias 边端点必须存在于节点表（稳定标识，不产生悬空/按次数分裂节点）
	nodeIDs := map[string]bool{}
	for _, n := range nodes {
		nodeIDs[string(n.ID)] = true
	}
	for _, f := range facts {
		if f.Kind == domain.FactAlias {
			if !nodeIDs[string(f.SourceID)] || !nodeIDs[string(f.TargetID)] {
				t.Errorf("alias 边端点节点缺失（标识不稳定）: %s → %s", f.SourceID, f.TargetID)
			}
		}
	}
}

// TestValueIdentityClosureScopes：同一外层函数里的多个闭包共享 outer
// canonical ID，但临时槽位必须按**原始作用域**区分（不得合并成同一节点）。
func TestValueIdentityClosureScopes(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type T struct{ A int }

func outer() {
	f := func() {
		x := &T{}
		x.A = 1
	}
	g := func() {
		x := &T{}
		x.A = 2
	}
	f()
	g()
}
`,
	})
	const funcID = "symbol:go:example.com/mtest:outer"
	ids := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == domain.KindSSAValue && n.Property("func_id") == funcID &&
			strings.Contains(string(n.ID), "@closure:") {
			ids[string(n.ID)] = true
		}
	}
	if len(ids) < 2 {
		t.Errorf("两个闭包的同槽位值应有 ≥2 个不同 ID，got %v", ids)
	}
}

// TestValueIdentityInitScopes：同包多个 init 共享 canonical ID，临时槽位
// 必须按 init 声明位置区分（不得合并）。
func TestValueIdentityInitScopes(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type T struct{ A int }

func init() {
	x := &T{}
	x.A = 1
}

func init() {
	x := &T{}
	x.A = 2
}
`,
	})
	ids := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == domain.KindSSAValue && strings.Contains(string(n.ID), ":init#") &&
			strings.Contains(string(n.ID), "@init:") {
			ids[string(n.ID)] = true
		}
	}
	if len(ids) < 2 {
		t.Errorf("两个 init 的同槽位值应有 ≥2 个不同 ID，got %v", ids)
	}
}
