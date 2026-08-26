package ast

import (
	"go/ast"
	"go/types"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// collectRegisterDefs 扫描模块内 Register 函数定义（与
// collectRegisterServers 同签名识别——isGrpcRegistrar/RegisterService
// 调用），额外提取 srv 接口类型。函数定义在 proto 生成代码
// （.pb.go）——外部仓库注册时定义仍在。
func collectRegisterDefs(pkgs []*packages.Package, modules []string) []grpcRegisterDef {
	var out []grpcRegisterDef
	for _, pkg := range pkgs {
		if !isInModule(pkg.PkgPath, modules) {
			continue
		}
		for _, f := range pkg.Syntax {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				obj := pkg.TypesInfo.Defs[fn.Name]
				if obj == nil {
					continue
				}
				sig, ok := obj.Type().(*types.Signature)
				if !ok || sig.Params().Len() < 2 {
					continue
				}
				if !isGrpcRegistrar(sig.Params().At(0).Type()) && !callsRegisterService(fn) {
					continue
				}
				svc := registerServiceName(pkg, fn, sig)
				if svc == "" {
					continue
				}

				iface, ok := sig.Params().At(1).Type().(*types.Named)
				if !ok {
					continue
				}
				if _, isIface := iface.Underlying().(*types.Interface); !isIface {
					continue
				}
				pos := pkg.Fset.PositionFor(fn.Pos(), false)
				out = append(out, grpcRegisterDef{
					svcName: svc, iface: iface,
					fnPkg: pkg.PkgPath, fnName: fn.Name.Name,
					line: pos.Line, file: pos.Filename,
				})
			}
		}
	}
	return out
}

// interfaceImplsInModule 全模块扫描实现接口的具体类型（types.Implements
// 指针/值方法集任一；排除接口自身与 Unimplemented 桩——注册实现才是
// 契约要的）。
func interfaceImplsInModule(pkgs []*packages.Package, modules []string, iface *types.Named) []*types.Named {
	var out []*types.Named
	for _, pkg := range pkgs {
		if !isInModule(pkg.PkgPath, modules) {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			if _, isIface := named.Underlying().(*types.Interface); isIface {
				continue
			}
			if strings.HasPrefix(named.Obj().Name(), "Unimplemented") {
				continue
			}
			ifaceType := iface.Underlying().(*types.Interface)
			if types.Implements(types.NewPointer(named), ifaceType) || types.Implements(named, ifaceType) {
				out = append(out, named)
			}
		}
	}
	return out
}

// emitGrpcServicesFromRegisters 从 Register 函数定义发射 grpc_service
// 节点 + registers_service 属性 + grpc_impl 边（实现类型 = 接口的
// types.Implements 实现——不依赖注册调用点；调用点路径发射的同名
// 节点/边 UPSERT 幂等合并）。
func emitGrpcServicesFromRegisters(repo *domain.Repository, pkgs []*packages.Package, emit domain.EmitFunc) {
	for _, def := range collectRegisterDefs(pkgs, repo.Modules) {
		svcID := domain.CanonicalID("symbol:go:" + def.fnPkg + ":svc." + def.svcName)
		_ = emit(domain.Item{Node: &domain.CodeEntity{
			ID:         svcID,
			Kind:       domain.KindGrpcService,
			Name:       "svc." + def.svcName,
			Properties: map[string]any{"service_name": def.svcName},
		}})

		_ = emit(domain.Item{Node: &domain.CodeEntity{
			ID:   domain.CanonicalID("symbol:go:" + def.fnPkg + ":" + def.fnName),
			Kind: domain.KindFunction, Name: def.fnName,
			FilePath:   relPath(repo.Path, def.file),
			LineStart:  def.line,
			Properties: map[string]any{"registers_service": def.svcName},
		}})

		for _, impl := range interfaceImplsInModule(pkgs, repo.Modules, def.iface) {
			implID := domain.CanonicalID("symbol:go:" + impl.Obj().Pkg().Path() + ":" + impl.Obj().Name())
			_ = emit(domain.Item{Node: &domain.CodeEntity{
				ID:   implID,
				Kind: domain.KindStruct,
				Name: impl.Obj().Name(),
			}})
			_ = emit(domain.Item{Fact: &domain.Fact{
				SourceID:   implID,
				TargetID:   svcID,
				Kind:       domain.FactGrpcImpl,
				ToolSource: domain.ToolCodeGraph,
				Confidence: 0.9,
			}})
		}
	}
}
