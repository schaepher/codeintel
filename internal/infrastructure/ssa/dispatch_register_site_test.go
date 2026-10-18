package ssa

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestDispatchRegisterSiteStable：同一接口方法由**值与指针两个注册点**
// 命中（同一 method 对象）时，dispatch_to 的 register_site 必须是稳定值
// （最小值）——不能随 regs map 的迭代顺序抖动（R10）。
func TestDispatchRegisterSiteStable(t *testing.T) {
	files := map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Iface interface{ M() }

type Impl struct{}

func (i Impl) M() {}

func regA() Iface { return &Impl{} }

func regB() Iface { return Impl{} }

func use(i Iface) { i.M() }
`,
	}
	// fixture 行号：regA 的 &Impl{} 在第 9 行、regB 的 Impl{} 在第 11 行
	// （上方为文件内容）——稳定规则取最小（9）。
	const wantSite = 9
	seen := map[int]bool{}
	for i := 0; i < 25; i++ {
		_, facts := indexFixture(t, files)
		var site int
		for _, f := range facts {
			if f.Kind != domain.FactDispatchTo || !strings.HasSuffix(string(f.TargetID), ":(Impl).M") {
				continue
			}
			switch v := f.Metadata["register_site"].(type) {
			case int:
				site = v
			case float64:
				site = int(v)
			}
		}
		if site == 0 {
			t.Fatalf("第 %d 次：未取到 register_site（dispatch_to 边缺失）", i)
		}
		seen[site] = true
		if site != wantSite {
			t.Errorf("register_site = %d, want %d（稳定取最小注册行号）", site, wantSite)
		}
	}
	if len(seen) != 1 {
		t.Errorf("register_site 出现多个值（不确定）: %v", seen)
	}
}

// TestDispatchRegisterSiteValuePointerSharedMethod：值与指针共享同一方法
// 时只产出一条 dispatch_to（candidateKey 相同）且站点稳定。
func TestDispatchRegisterSiteValuePointerSharedMethod(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Iface interface{ M() }

type Impl struct{}

func (i Impl) M() {}

func regA() Iface { return &Impl{} }

func regB() Iface { return Impl{} }

func use(i Iface) { i.M() }
`,
	})
	n := 0
	for _, f := range facts {
		if f.Kind == domain.FactDispatchTo && strings.HasSuffix(string(f.TargetID), ":(Impl).M") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("值与指针共享方法应只产 1 条 dispatch_to，got %d", n)
	}
}

// TestDispatchRegisterSiteFileStable：不同文件、同一行号的注册点——按
// (文件, 行号) 字典序稳定取最小，并在 metadata 记录 register_file
// （只有行号无法区分不同文件，todo2-7）。
func TestDispatchRegisterSiteFileStable(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"a.go": `package m

func regA() Iface { return &Impl{} }

type Iface interface{ M() }

type Impl struct{}

func (i Impl) M() {}
`,
		"b.go": `package m

func regB() Iface { return Impl{} }

func use(i Iface) { i.M() }
`,
	})
	n := 0
	var file string
	var site int
	for _, f := range facts {
		if f.Kind != domain.FactDispatchTo || !strings.HasSuffix(string(f.TargetID), ":(Impl).M") {
			continue
		}
		n++
		if v, ok := f.Metadata["register_file"].(string); ok {
			file = v
		}
		switch v := f.Metadata["register_site"].(type) {
		case int:
			site = v
		case float64:
			site = int(v)
		}
	}
	if n != 1 {
		t.Fatalf("值与指针共享方法应只产 1 条 dispatch_to，got %d", n)
	}
	// 两个注册点都在第 3 行；文件字典序 a.go < b.go
	if file != "a.go" || site != 3 {
		t.Errorf("register = (%q,%d), want (a.go,3)（稳定取文件字典序最小）", file, site)
	}
}

// TestDispatchEmbeddedSharedMethod：不同注册类型通过**嵌入**共享同一方法
// （Outer 提升 Base.M）——只产一条 dispatch_to，候选键按实际方法（Base.M）。
func TestDispatchEmbeddedSharedMethod(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

type Base struct{}

func (b Base) M() {}

type Outer struct{ Base }

type Iface interface{ M() }

func regA() Iface { return &Outer{} }

func regB() Iface { return Base{} }

func use(i Iface) { i.M() }
`,
	})
	n := 0
	for _, f := range facts {
		if f.Kind == domain.FactDispatchTo && strings.HasSuffix(string(f.TargetID), ":(Base).M") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("嵌入共享方法应只产 1 条 dispatch_to（(Base).M），got %d", n)
	}
}
