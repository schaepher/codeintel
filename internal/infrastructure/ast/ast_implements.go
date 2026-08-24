package ast

// R37 编译期接口断言扫描：`var _ Iface = expr`（expr 动态类型为具体类型）
// → types.Implements 验证 → emit implements 边（接口 → 实现者，conf 0.8）。
// 背景：scip-go 对断言声明不输出 is_implementation（go2o 实测：queryService
// 仅靠 `var _ proto.QueryServiceServer = new(queryService)` 声明实现关系，
// implements 边缺失 → grpc-impl 追业务实现失败）。此为 SCIP 盲区通用补丁。

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// emitInterfaceAssertions 文件级扫描包级 `var _ Iface = expr` 断言。
// 形态：断言左侧类型是接口（Named.Underlying Interface）；右侧表达式
// 动态类型是具体类型（指针解包；右侧本身是接口 → 无实现者信息跳过）；
// types.Implements 指针/值方法集任一验证通过 → emits implements 边。
// 端点节点缺失时补 emit（fixture 无 SCIP；真实索引 UPSERT 合并）。
func emitInterfaceAssertions(repo *domain.Repository, pkg *packages.Package, f *ast.File, emit domain.EmitFunc) error {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "_" || len(vs.Values) != 1 {
				continue
			}
			// 断言左侧类型：必须是接口（Named → Interface）；
			// 内置接口（error 等 types.Universe）Pkg() 为 nil 跳过
			ifaceType := pkg.TypesInfo.TypeOf(vs.Type)
			ifaceNamed, ok := ifaceType.(*types.Named)
			if !ok || ifaceNamed.Obj().Pkg() == nil {
				continue
			}
			iface, ok := ifaceNamed.Underlying().(*types.Interface)
			if !ok || iface.NumMethods() == 0 {
				continue
			}
			// 右侧表达式动态类型：具体类型（指针解包）；接口类型跳过
			dyn := pkg.TypesInfo.TypeOf(vs.Values[0])
			impl := dyn
			if p, ok := dyn.(*types.Pointer); ok {
				impl = p.Elem()
			}
			implNamed, ok := impl.(*types.Named)
			if !ok || implNamed.Obj().Pkg() == nil {
				continue
			}
			if _, isIface := implNamed.Underlying().(*types.Interface); isIface {
				continue
			}
			// 实现验证：指针/值方法集任一（new(T) 是 *T；T{} 是 T）
			if !types.Implements(types.NewPointer(implNamed), iface) && !types.Implements(implNamed, iface) {
				continue
			}
			// implements 边（接口 → 实现者——与 SCIP is_implementation 同向）
			ifaceID := domain.CanonicalID("symbol:go:" + ifaceNamed.Obj().Pkg().Path() + ":" + ifaceNamed.Obj().Name())
			implID := domain.CanonicalID("symbol:go:" + implNamed.Obj().Pkg().Path() + ":" + implNamed.Obj().Name())
			if err := emit(domain.Item{Node: &domain.CodeEntity{
				ID: ifaceID, Kind: domain.KindInterface, Name: ifaceNamed.Obj().Name(),
			}}); err != nil {
				return err
			}
			implKind := domain.KindStruct
			if _, isStruct := implNamed.Underlying().(*types.Struct); !isStruct {
				implKind = domain.KindInterface
			}
			if err := emit(domain.Item{Node: &domain.CodeEntity{
				ID: implID, Kind: implKind, Name: implNamed.Obj().Name(),
			}}); err != nil {
				return err
			}
			if err := emit(domain.Item{Fact: &domain.Fact{
				SourceID: ifaceID, TargetID: implID,
				Kind: domain.FactImplements, Confidence: 0.8,
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}
