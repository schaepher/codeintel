package ast

// R49 接口完整包含 grpc server 接口检测（用户要求：找哪些接口完整地
// 包含了 .pb.go 里 server 的接口——这些接口是重要信息，可能是组合/
// 扩展多个 grpc 服务的聚合接口）。判定：项目内接口的方法名集 ⊇
// .pb.go 里 XxxServer 接口的方法名集（超集——完整包含）。

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// collectPBServers 收集所有 .pb.go 文件里 XxxServer 结尾接口的方法名集
// （protoc 生成的服务端接口——被包含的目标）。键：pkgPath:ifaceName。
func collectPBServers(pkgs []*packages.Package, modules []string) map[string][]string {
	out := map[string][]string{}
	for _, pkg := range pkgs {
		if !isInModule(pkg.PkgPath, modules) {
			continue
		}
		for _, f := range pkg.Syntax {
			fname := pkg.Fset.PositionFor(f.Pos(), false).Filename
			if !strings.HasSuffix(fname, ".pb.go") {
				continue
			}
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					iface, ok := ts.Type.(*ast.InterfaceType)
					if !ok || !strings.HasSuffix(ts.Name.Name, "Server") {
						continue
					}
					var methods []string
					for _, m := range iface.Methods.List {
						if len(m.Names) > 0 {
							methods = append(methods, m.Names[0].Name)
						}
					}
					if len(methods) > 0 {
						out[pkg.PkgPath+":"+ts.Name.Name] = methods
					}
				}
			}
		}
	}
	return out
}

// interfaceMethodsByTypes 接口完整方法名（types 层——内嵌接口展开；
// AST 层嵌入字段 Names 为空会漏方法）。
func interfaceMethodsByTypes(pkg *packages.Package, ts *ast.TypeSpec) []string {
	obj := pkg.TypesInfo.Defs[ts.Name]
	if obj == nil {
		return nil
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	var methods []string
	for i := 0; i < iface.NumMethods(); i++ {
		methods = append(methods, iface.Method(i).Name())
	}
	return methods
}

// methodSuperset 方法集 a ⊇ b（b 全部在 a 中）。
func methodSuperset(a, b []string) bool {
	set := map[string]bool{}
	for _, m := range a {
		set[m] = true
	}
	for _, m := range b {
		if !set[m] {
			return false
		}
	}
	return true
}

// emitInterfaceContainers 扫描接口完整包含 pb server 接口（方法名超
// 集——types 层展开内嵌）→ 接口节点属性 pb_servers（R49：组合/扩展
// grpc 服务的重要信息）。排除 .pb.go 里定义的接口（protoc 生成结构
// 噪音——XxxServiceServer 内嵌 Unsafe 的自包含关系无业务信息）。
func (a *Adapter) emitInterfaceContainers(repo *domain.Repository, pkg *packages.Package, f *ast.File, emit domain.EmitFunc) error {
	if len(a.pbServers) == 0 {
		return nil
	}
	fname := pkg.Fset.PositionFor(f.Pos(), false).Filename
	if strings.HasSuffix(fname, ".pb.go") {
		return nil // protoc 生成接口——自包含/嵌入关系是生成结构噪音
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, ok := ts.Type.(*ast.InterfaceType); !ok {
				continue
			}
			methods := interfaceMethodsByTypes(pkg, ts)
			if len(methods) == 0 {
				continue
			}
			var contained []string
			for name, sm := range a.pbServers {
				if methodSuperset(methods, sm) {
					contained = append(contained, name)
				}
			}
			if len(contained) == 0 {
				continue
			}
			sort.Strings(contained)
			ifaceID := domain.CanonicalID("symbol:go:" + pkg.PkgPath + ":" + ts.Name.Name)
			if err := emit(domain.Item{Node: &domain.CodeEntity{
				ID: ifaceID, Kind: domain.KindInterface, Name: ts.Name.Name,
				Properties: map[string]any{"pb_servers": strings.Join(contained, ",")},
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}
