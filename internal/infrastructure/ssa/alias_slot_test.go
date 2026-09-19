package ssa

import (
	"sort"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestAnonAllocSingleValueNode（Q252b 回归）：同一 SSA 值在**两条发射路径**
// 上必须落同一节点——匿名分配（`t := &T{}`，SSA 名 tN）此前分裂为
//
//	main#t0        （alias pass 发：只有 alias 边）
//	main#*mtest.T  （fe 发：只有 argument/returns 边）
//
// 于是 query path / value-trace 的跨函数链在匿名分配处断开
// （集成 TestQueryPathSelfContained / TestCLIFullFlowPart2 实测）。
//
// 不变量：main 内作为 alias 边端点的值节点，必须同时是 argument 边端点。
func TestAnonAllocSingleValueNode(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type T struct{ Key string }

func fill(t *T) { t.Key = "x" }

func use(t *T) { _ = t.Key }

func main() {
	t := &T{}
	fill(t)
	use(t)
}
`,
	})
	mainPrefix := "symbol:go:example.com/mtest:main#"
	aliasEnds := map[string]bool{}
	argEnds := map[string]bool{}
	for _, f := range facts {
		switch f.Kind {
		case domain.FactAlias:
			if strings.HasPrefix(string(f.TargetID), mainPrefix) {
				aliasEnds[string(f.TargetID)] = true
			}
		case domain.FactArgument:
			if strings.HasPrefix(string(f.SourceID), mainPrefix) {
				argEnds[string(f.SourceID)] = true
			}
		}
	}
	if len(aliasEnds) == 0 {
		t.Fatalf("fixture 未触发 alias 边（main 无 aliased 对象）")
	}
	if len(argEnds) == 0 {
		t.Fatalf("fixture 未触发 argument 边（main 未传参）")
	}
	for id := range aliasEnds {
		if !argEnds[id] {
			t.Errorf("匿名分配节点分裂：alias 端点 %q，argument 端点 %v（应同一节点）",
				id, sortedKeys(argEnds))
		}
	}
	// 反向也要成立：不存在只挂 argument 边的同类型孪生节点
	for id := range argEnds {
		if !aliasEnds[id] {
			t.Errorf("匿名分配节点分裂：argument source %q 无对应 alias 端点 %v",
				id, sortedKeys(aliasEnds))
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
