package ast

import (
	"context"
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
	"golang.org/x/tools/go/packages"
)

// Index 加载仓库全部包并产出 CALLS / IMPORTS 边。
func (a *Adapter) Index(ctx context.Context, repo *domain.Repository, pkgs []*packages.Package, emit domain.EmitFunc) error {
	logger := logging.FromContext(ctx)
	logger.Debug("enter (Adapter).Index")
	defer logger.Debug("exit (Adapter).Index")
	packages.PrintErrors(pkgs)

	a.pkgsByPath = map[string]*packages.Package{}
	a.modules = repo.Modules
	for _, p := range pkgs {
		a.pkgsByPath[p.PkgPath] = p
	}
	// R49：预扫 .pb.go 的 XxxServer 接口（接口完整包含检测的跨包目标）
	a.pbServers = collectPBServers(pkgs, repo.Modules)

	serviceFlags := map[domain.CanonicalID]map[string]bool{}

	registerServers := collectRegisterServers(pkgs, repo.Modules)
	// R91：注册佐证集合（服务名——接口签名识别的真服务判定）
	a.registeredServices = map[string]bool{}
	for _, svc := range registerServers {
		a.registeredServices[svc] = true
	}
	for svc := range collectDirectRegisters(pkgs, repo.Modules) {
		a.registeredServices[svc] = true
	}
	// R86：外部注册场景（Register 调用点在其他仓库单独编译）——从
	// Register 函数定义（.pb.go 签名）发射 grpc_service 节点 +
	// grpc_impl 边（types.Implements 找实现——不依赖调用点）
	emitGrpcServicesFromRegisters(repo, pkgs, emit)

	newClients := collectNewClients(pkgs, repo.Modules)

	a.routes = nil
	routes, routeWarns := loadRoutes(repo.Path)
	for _, w := range routeWarns {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	for _, rt := range routes {
		if rt.Path == "" || rt.Handler == "" {
			continue
		}
		hID, ok := a.resolveRouteHandler(repo, rt.Handler)
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: routes.yaml handler 未找到: %s\n", rt.Handler)
			continue
		}
		nodeID := domain.CanonicalID("symbol:go:" + pkgOfID(hID) + ":route." + rt.Path)
		_ = emit(domain.Item{Node: &domain.CodeEntity{
			ID:   nodeID,
			Kind: domain.KindHTTPRoute,
			Name: "route." + rt.Path,
			Properties: map[string]any{
				"path":       rt.Path,
				"method":     rt.Method,
				"handler_id": string(hID),
			},
		}})
		a.routes = append(a.routes, routeEntry{path: rt.Path, nodeID: nodeID})
	}

	for _, pkg := range pkgs {
		if !isInModule(pkg.PkgPath, repo.Modules) {
			continue
		}
		if err := a.processPackage(repo, pkg, emit, serviceFlags, registerServers, newClients); err != nil {
			return err
		}
	}
	return nil
}
func (a *Adapter) processPackage(repo *domain.Repository, pkg *packages.Package, emit domain.EmitFunc,
	serviceFlags map[domain.CanonicalID]map[string]bool, registerServers map[string]string,
	newClients map[string]string) error {
	logger := zap.L()
	logger.Debug("enter (Adapter).processPackage")
	defer logger.Debug("exit (Adapter).processPackage")
	if err := ensurePackageNode(repo, pkg, emit); err != nil {
		return err
	}

	if err := a.markHTTPHandlers(repo, pkg, emit); err != nil {
		return err
	}

	// R30-2：接口方法签名识别 grpc 服务接口（不依赖注册点/函数名）
	if err := a.markGrpcServiceInterfaces(repo, pkg, emit); err != nil {
		return err
	}

	// R35：urfave/cli v2 命令树识别（不依赖文件路径——代码事实发现）
	if err := a.markCLICommands(repo, pkg, emit); err != nil {
		return err
	}

	for importPath := range pkg.Imports {
		if !isInModule(importPath, repo.Modules) {
			continue
		}
		if err := emit(domain.Item{Fact: &domain.Fact{
			SourceID:   packageID(pkg.PkgPath),
			TargetID:   packageID(importPath),
			Kind:       domain.FactImports,
			ToolSource: domain.ToolCodeGraph,
			Confidence: 0.8,
		}}); err != nil {
			return err
		}
	}

	for _, f := range pkg.Syntax {
		if err := a.processFile(repo, pkg, f, emit, serviceFlags, registerServers, newClients); err != nil {
			return err
		}
	}
	return nil
}
