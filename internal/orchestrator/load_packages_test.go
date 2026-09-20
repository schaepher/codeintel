package orchestrator

// Q247 契约测试：loadPackages 的依赖信息契约——**模块内包给
// Syntax+TypesInfo，依赖包只给 Types（AST/TypesInfo 不保证）**。
//
// 背景（docs/design-q247.md §1）：Mode 里带 packages.NeedDeps 会让
// go/packages 把整个传递依赖图的 AST/TypesInfo 也解析并常驻——go2o
// 实测 774 个 reachable 包全部带 AST（3718 语法文件）/ 存活堆 955MB，
// 去掉后 137 包 / 526 文件 / 148MB；紧随其后的「释放依赖 AST」循环只
// 作用于返回切片（全是模块包）故为空操作。
//
// 本测试用**本仓库自身**作 fixture（真实 module + 真实外部依赖，无需
// 网络/临时模块）：① 模块包必须全量可分析；② 外部依赖的 Types 必须
// 可达（ast_grpc_collect 的 R90 外部注册识别按 types 设计）；
// ③ 外部依赖不得带 Syntax（回归护栏——防有人把 NeedDeps 加回来）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// probeInModule 与 ssa.isInModule 同语义（自身或子包前缀匹配）。
func probeInModule(pkgPath string, modules []string) bool {
	for _, m := range modules {
		if m == "" {
			continue
		}
		if pkgPath == m || strings.HasPrefix(pkgPath, m+"/") {
			return true
		}
	}
	return false
}

func TestLoadPackagesDependencyContract(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// 直接用本仓库根 module（不经 DiscoverModules：仓库根的 .tmp/ 是
	// 本项目的临时目录/TMPDIR，并发测试会往里建临时 module——那种目录
	// 不能当索目标）
	mf, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	mod := ""
	for _, line := range strings.Split(string(mf), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			mod = strings.TrimSpace(rest)
			break
		}
	}
	if mod == "" {
		t.Fatal("go.mod 无 module 指令")
	}
	modules := []string{mod}
	o := &Orchestrator{Repo: &domain.Repository{
		Path: root, Module: mod, Modules: modules, ModuleDirs: []string{"."},
	}}
	pkgs, err := o.loadPackages(context.Background(), nil)
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("loadPackages 未返回任何包（fixture 失效）")
	}

	// ① 模块内包：Syntax + TypesInfo + Types 齐全（适配器全量分析前提）
	modulePkgs := 0
	for _, p := range pkgs {
		if !probeInModule(p.PkgPath, modules) {
			t.Errorf("loadPackages 返回了非模块内包 %s（应只返回 pattern 匹配的模块包）", p.PkgPath)
			continue
		}
		modulePkgs++
		if p.Syntax == nil || p.TypesInfo == nil || p.Types == nil {
			t.Errorf("模块内包 %s 应带 Syntax/TypesInfo/Types，got syntax=%v typesinfo=%v types=%v",
				p.PkgPath, p.Syntax != nil, p.TypesInfo != nil, p.Types != nil)
		}
	}

	// ②③ 依赖包：Types 可达、Syntax 不得有
	reach := map[string]*packages.Package{}
	var walk func(p *packages.Package)
	walk = func(p *packages.Package) {
		if p == nil || reach[p.ID] != nil {
			return
		}
		reach[p.ID] = p
		for _, dp := range p.Imports {
			walk(dp)
		}
	}
	for _, p := range pkgs {
		walk(p)
	}
	extTotal, extNoTypes, extWithSyntax := 0, []string{}, []string{}
	for _, p := range reach {
		if probeInModule(p.PkgPath, modules) {
			continue
		}
		// 无 CompiledGoFiles 的编译器伪包（unsafe——GOROOT 里只有 unsafe.go
		// 且由编译器特例提供类型）不参与 Syntax 断言
		if len(p.CompiledGoFiles) == 0 {
			continue
		}
		extTotal++
		if p.Types == nil {
			extNoTypes = append(extNoTypes, p.PkgPath)
		}
		if p.Syntax != nil {
			extWithSyntax = append(extWithSyntax, p.PkgPath)
		}
	}
	if extTotal == 0 {
		t.Fatal("reachable 图里没有外部依赖包（用例失效，无法验证契约）")
	}
	if len(extNoTypes) > 0 {
		t.Errorf("外部依赖包必须带 Types（类型契约，R90 外部注册识别依赖它），%d 个缺失：%v",
			len(extNoTypes), extNoTypes)
	}
	if len(extWithSyntax) > 0 {
		t.Errorf("外部依赖包不得带 Syntax（Q247：NeedDeps 会把传递依赖图的 AST 全量常驻，go2o 实测 808MB），%d 个违规：%v",
			len(extWithSyntax), extWithSyntax)
	}
	t.Logf("契约通过：模块包 %d 个（全量）；reachable 外部包 %d 个（Types 齐全、无 Syntax）",
		modulePkgs, extTotal)
}

// Q251：多 module 必须共用**一个** token.FileSet——原先每个 module 各一次
// packages.Load（各自新建 Fset），而 ssautil.Packages 的 SSA Program 只用
// initial[0].Fset：非首模块的 token.Pos 在错误 Fset 里解析（461 行文件报
// 498 行），relPath 失败导致 file_path 丢失（增量按文件删除匹配不到）。
//
// 断言：① 返回包两两 Fset 同一实例；② 嵌套 module 的文件位置能用该 Fset
// 解析出正确文件（路径含嵌套目录、行号 ≤ 文件行数）。
func TestLoadPackagesSharedFileSet(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	// 嵌套 module（第二个 Load）
	writeFile(t, filepath.Join(dir, "sub", "go.mod"), "module example.com/sub\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "sub", "sub.go"),
		"package sub\n\nimport \"fmt\"\n\nfunc Hello() string {\n\tfmt.Println(\"x\")\n\treturn \"hi\"\n}\n")

	modules, dirs, err := DiscoverModules(dir)
	if err != nil {
		t.Fatalf("DiscoverModules: %v", err)
	}
	o := &Orchestrator{Repo: &domain.Repository{
		Path: dir, Module: modules[0], Modules: modules, ModuleDirs: dirs,
	}}
	pkgs, err := o.loadPackages(context.Background(), nil)
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	if len(pkgs) < 2 {
		t.Fatalf("应加载到根 module + 嵌套 module 的包，got %d", len(pkgs))
	}
	fset := pkgs[0].Fset
	if fset == nil {
		t.Fatal("包的 Fset 不应为空")
	}
	for _, p := range pkgs {
		if p.Fset != fset {
			t.Fatalf("包 %s 的 Fset 与其他包不同实例（Q251：必须共享一个）", p.PkgPath)
		}
	}
	// 嵌套 module 的文件位置可正确解析
	for _, p := range pkgs {
		if p.PkgPath != "example.com/sub" {
			continue
		}
		obj := p.Types.Scope().Lookup("Hello")
		if obj == nil {
			t.Fatal("sub.Hello 未找到")
		}
		pos := fset.PositionFor(obj.Pos(), false)
		if !strings.HasSuffix(pos.Filename, filepath.Join("sub", "sub.go")) {
			t.Errorf("Hello 的位置 = %s，应落在 sub/sub.go", pos.Filename)
		}
		if pos.Line < 5 || pos.Line > 8 {
			t.Errorf("Hello 行号 = %d，应在 5-8（位置解析错乱）", pos.Line)
		}
	}
}

// Q252f：ModuleDirs 为空必须大声报错（否则 loadPackages 返回空包集，
// AST/SSA 全空跑而构建报 success——静默退化）。
func TestLoadPackagesRejectsEmptyModuleDirs(t *testing.T) {
	o := &Orchestrator{Repo: &domain.Repository{Path: t.TempDir(), Module: "example.com/x", Modules: []string{"example.com/x"}}}
	_, err := o.loadPackages(context.Background(), nil)
	if err == nil {
		t.Fatal("ModuleDirs 为空应当报错（拒绝静默返回空包集）")
	}
	if !strings.Contains(err.Error(), "ModuleDirs") {
		t.Fatalf("错误信息应点明 ModuleDirs：%v", err)
	}
}

// Q252f：全量构建零包同样报错（增量构建的 patterns 语义另测——那里
// “无变更包”合法）。
func TestLoadPackagesFullBuildZeroPackagesFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/empty\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "readme.txt"), "no go files here")
	o := &Orchestrator{Repo: &domain.Repository{Path: dir, Module: "example.com/empty", Modules: []string{"example.com/empty"}, ModuleDirs: []string{"."}}}
	_, err := o.loadPackages(context.Background(), nil)
	if err == nil {
		t.Fatal("全量构建零包应当报错")
	}
}
