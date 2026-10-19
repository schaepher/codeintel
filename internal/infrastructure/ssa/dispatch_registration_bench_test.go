package ssa

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// benchSnapshotProg 构建快照 + Program（派发注册收集基准用）。
func benchSnapshotProg(b *testing.B, files map[string]string) (*funcSnapshot, *ssa.Program) {
	b.Helper()
	dir := b.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		b.Fatalf("load: %v", err)
	}
	prog, _ := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		b.Fatal("ssautil.Packages 失败")
	}
	prog.Build()
	return newFuncSnapshot(prog), prog
}

// registrationFixture 生成 n 个静态转换（同一接口/具体类型，不同行）。
func registrationFixture(n int) map[string]string {
	var sb strings.Builder
	sb.WriteString("package m\n\ntype Iface interface{ M() }\n\ntype Impl struct{}\n\nfunc (i Impl) M() {}\n\nfunc reg() {\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "\t_ = Iface(&Impl{}) // %d\n", i)
	}
	sb.WriteString("}\n\nfunc main() { reg() }\n")
	return map[string]string{"go.mod": moduleGoMod, "main.go": sb.String()}
}

func benchCollectDispatchRegistrations(b *testing.B, n int) {
	s, prog := benchSnapshotProg(b, registrationFixture(n))
	mods := []string{"example.com/mtest"}
	modFuncs := s.moduleFuncs(mods)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		regs, pkgs := collectDispatchRegistrations(prog, modFuncs, mods, s, nil)
		if len(pkgs) == 0 {
			b.Fatal("no reg pkgs")
		}
		_ = regs
	}
}

// BenchmarkCollectDispatchSparse 稀疏注册（<阈值，线性去重）。
func BenchmarkCollectDispatchSparse(b *testing.B) { benchCollectDispatchRegistrations(b, 4) }

// BenchmarkCollectDispatchDense 密集注册（≥阈值，集合去重；分配单独记录）。
func BenchmarkCollectDispatchDense(b *testing.B) { benchCollectDispatchRegistrations(b, 40) }
