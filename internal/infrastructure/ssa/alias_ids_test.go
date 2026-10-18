package ssa

import (
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// testAliasPass 构建最小 aliasPass（只覆盖 valueNodeID 所需字段）。
func testAliasPass(t *testing.T, files map[string]string) *aliasPass {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, filepath.Join(dir, name), content)
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	prog, _ := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		t.Fatal("ssautil.Packages 失败")
	}
	prog.Build()
	return &aliasPass{
		repo:     &domain.Repository{Path: dir},
		prog:     prog,
		emit:     func(domain.Item) error { return nil },
		funcIDs:  map[*ssa.Function]domain.CanonicalID{},
		allocIDs: map[ssa.Value]domain.CanonicalID{},
		valueIDs: map[ssa.Value]domain.CanonicalID{},
		slotSeen: map[string]map[string]bool{},
		funcs:    newFuncSnapshot(prog),
	}
}

// TestValueNodeIDStableOnRepeat（todo3-4）：同一值重复查询必须返回同一
// 标识——不能因槽位访问状态第二次追加 @行号（与 emitValue 路径一致）。
func TestValueNodeIDStableOnRepeat(t *testing.T) {
	p := testAliasPass(t, map[string]string{
		"go.mod":  moduleGoMod,
		"main.go": "package m\n\nfunc f() int { return 1 }\n\nfunc main() { _ = f() }\n",
	})
	var v ssa.Value
	for _, fn := range p.funcs.all() {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				if call, ok := instr.(*ssa.Call); ok && call.Parent() != nil {
					v = call
					break
				}
			}
			if v != nil {
				break
			}
		}
		if v != nil {
			break
		}
	}
	if v == nil {
		t.Fatal("fixture 未找到调用值")
	}
	id1, ok := p.valueNodeID(v)
	if !ok {
		t.Fatal("首次查询失败")
	}
	id2, ok := p.valueNodeID(v)
	if !ok {
		t.Fatal("第二次查询失败")
	}
	if id1 != id2 {
		t.Fatalf("同一值重复查询 ID 漂移：%s vs %s（不应按槽位访问状态追加 @行号）", id1, id2)
	}
}
