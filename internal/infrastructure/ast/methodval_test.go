package ast

// R18 方法值传参测试：f(x.Method) 形态（如 ast.Inspect(f, ctx.visit)）
// 应建 调用者 → 方法 的 calls 边——对象方法被传入回调时，调用关系
// 真实存在（实体图 fileCtx 无入边问题的根因）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestMethodValueArgCalls：方法值传参建 calls 边（调用者 → 方法）。
func TestMethodValueArgCalls(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"main.go": `package m

import "sort"

type Ctx struct{}

func (c *Ctx) visit(x int) {}

func run() {
	ctx := &Ctx{}
	// 方法值传参：run 调用 visit（作为回调交给外部函数）
	_ = sort.Search(10, ctx.visit)
}
`,
	})
	// run → (Ctx).visit 的 calls 边应存在
	found := false
	for _, f := range facts {
		if f.Kind == domain.FactCalls &&
			strings.HasSuffix(string(f.SourceID), ":run") &&
			strings.Contains(string(f.TargetID), "(Ctx).visit") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("方法值传参应建 run→(Ctx).visit calls 边\nfacts: %v", factKinds(facts))
	}
	_ = nodes
}

func factKinds(facts []*domain.Fact) []string {
	var out []string
	for _, f := range facts {
		out = append(out, string(f.Kind)+":"+shortName(string(f.SourceID))+"→"+shortName(string(f.TargetID)))
	}
	return out
}

func shortName(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[i+1:]
	}
	return id
}
