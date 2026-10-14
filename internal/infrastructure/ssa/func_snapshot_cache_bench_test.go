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

// benchSnapshot 在基准里构建 funcSnapshot（含函数体）。
func benchSnapshot(b *testing.B, files map[string]string) *funcSnapshot {
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
	return newFuncSnapshot(prog)
}

// benchCallSiteFixture 生成 fixture：K 个函数都调用共享 helper，另有 K 个
// 目标函数（用于模拟「每个目标函数重扫一次」）。
func benchCallSiteFixture(k int) map[string]string {
	var sb strings.Builder
	sb.WriteString("package main\n\nfunc helper(x int) int { return x + 1 }\n\n")
	for i := 0; i < k; i++ {
		fmt.Fprintf(&sb, "func caller%d() { _ = helper(%d) }\n", i, i)
	}
	sb.WriteString("\nfunc target() int { x := 0\n")
	for i := 0; i < k; i++ {
		fmt.Fprintf(&sb, "\tcaller%d(); x += %d\n", i, i)
	}
	sb.WriteString("\treturn x }\n\nfunc main() { _ = target() }\n")
	return map[string]string{"go.mod": baseModule, "main.go": sb.String()}
}

// BenchmarkStaticCallSitesLegacyPerTarget：旧实现按目标函数重扫全程序。
// 模拟 K 个目标函数（这里取 caller 数）各自一次全程序扫描。
func BenchmarkStaticCallSitesLegacyPerTarget(b *testing.B) {
	const k = 100
	s := benchSnapshot(b, benchCallSiteFixture(k))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < k; j++ {
			_ = legacyStaticCallSites(s) // 每个目标函数一次 = 全程序扫描
		}
	}
}

// BenchmarkStaticCallSitesIndexBuild：构建级索引一次 + O(1) 查询。
func BenchmarkStaticCallSitesIndexBuild(b *testing.B) {
	const k = 100
	s := benchSnapshot(b, benchCallSiteFixture(k))
	var helper *ssa.Function
	for _, f := range s.all() {
		if f.Name() == "helper" {
			helper = f
			break
		}
	}
	if helper == nil {
		b.Fatal("fixture 无 helper")
	}
	idx := s.staticCallSites() // 建一次
	if len(idx[helper]) == 0 {
		b.Fatal("索引无 helper 调用点")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < k; j++ {
			_ = len(s.staticCallSites()[helper]) // 命中缓存：无扫描
		}
	}
}
