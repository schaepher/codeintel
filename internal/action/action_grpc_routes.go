package action

// R92 迁移：`query grpc-routes` 查询逻辑（原 cli/query_grpc_routes.go）
// ——服务端 gRPC 路由清单。从已有节点发现：grpc_service 节点（markServiceEntry
// 注册点建立）→ RegisterXxxServer 函数（生成代码 .pb.go）→ 注册调用
// 点（calls 入边 line_num）+ grpc_impl 边（实现类型）+ ServiceDesc
// 全量方法（go/parser 提取生成代码——grpc_call 边只含被调用过的方法，
// 服务端定义全集在 ServiceDesc）。cli 只做参数转发与输出（cmdGrpcRoutes）；
// wiki/mcp 经 Actions.GrpcRoutes 同源调用。

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GrpcRouteMethod 一个服务方法（ServiceDesc 定义全集）。
type GrpcRouteMethod struct {
	Name    string `json:"name"`
	Handler string `json:"handler"`
}

// GrpcRouteService 一个 gRPC 服务（服务端路由）。
type GrpcRouteService struct {
	Name     string            `json:"name"`      // 服务名（QueryService）
	Impl     string            `json:"impl"`      // 实现类型（grpc_impl 边）
	ImplID   string            `json:"impl_id"`   // 实现类型 canonical ID（R37 流程页构造方法入口）
	ImplFile string            `json:"impl_file"` // 实现位置（file:line）
	Register string            `json:"register"`  // 注册调用点（file:line）
	Methods  []GrpcRouteMethod `json:"methods"`   // 服务方法全集（ServiceDesc）
}

// GrpcRoutesResult 查询结果（Q5 契约）。
type GrpcRoutesResult struct {
	Services []GrpcRouteService `json:"services"`
}

// GrpcRoutes 服务端路由清单：grpc_service 节点 → Register 函数 →
// 调用点/实现/方法（repoAbs 用于 ServiceDesc 生成代码解析）。
func (a *Actions) GrpcRoutes(repoAbs string) (*GrpcRoutesResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).GrpcRoutes", zap.String("repo_abs", repoAbs))
	defer logger.Info("exit (Actions).GrpcRoutes")
	return grpcRoutes(a.repo, repoAbs)
}

// grpcRoutes 服务端路由清单（r = 仓储窄接口；repoAbs = 仓库绝对路径）。
func grpcRoutes(r Reader, repoAbs string) (*GrpcRoutesResult, error) {
	res := &GrpcRoutesResult{Services: []GrpcRouteService{}}
	// 1. 全部 grpc_service 节点（服务名）
	svcs, err := r.GetGrpcServices()
	if err != nil {
		return nil, err
	}
	for _, n := range svcs {
		name := n.Property("service_name")
		if name == "" {
			name = strings.TrimPrefix(string(n.ID), "symbol:")
		}
		out := GrpcRouteService{Name: name, Methods: []GrpcRouteMethod{}}
		// 2. Register 函数（签名识别建立——按 registers_service 属性查，
		// R30：不再按函数名 RegisterXxxServer 推导）
		regFile := ""
		if f, ok := RegisterNodeFile(r, name); ok {
			regFile = f
		}
		// 3. 注册调用点：Register 函数的 calls 入边 → source 函数 file:line
		if src, line, ok := RegisterCallSite(r, name); ok {
			out.Register = fmt.Sprintf("%s:%d", src, line)
		}
		// 4. 实现类型：grpc_impl 边（impl → 服务）
		if impl, implID, implFile, ok := GrpcImpl(r, string(n.ID)); ok {
			out.Impl = impl
			out.ImplID = implID
			out.ImplFile = implFile
		}
		// 5. 方法全集：ServiceDesc（生成代码文件）优先（含 handler）；
		// 手写服务无 ServiceDesc → 服务节点 methods 属性（R30-2 接口
		// 签名识别写入，逗号分隔）
		if regFile != "" {
			if m := serviceDescMethods(filepath.Join(repoAbs, regFile), name); len(m) > 0 {
				out.Methods = m
			}
		}
		if len(out.Methods) == 0 {
			if names := SvcMethodsProp(r, string(n.ID)); len(names) > 0 {
				for _, mn := range names {
					out.Methods = append(out.Methods, GrpcRouteMethod{Name: mn})
				}
			}
		}
		res.Services = append(res.Services, out)
	}
	sort.Slice(res.Services, func(i, j int) bool { return res.Services[i].Name < res.Services[j].Name })
	return res, nil
}

// SvcMethodsProp 服务节点 methods 属性（R30-2 接口签名识别写入，
// 逗号分隔——手写服务无 ServiceDesc 时的方法源）。
func SvcMethodsProp(r Reader, svcID string) []string {
	n, err := r.GetSymbol(domain.CanonicalID(svcID))
	if err != nil || n == nil {
		return nil
	}
	v := n.Property("methods")
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// RegisterNodeFile 按 registers_service 属性查注册函数文件（R30：
// 签名识别建立——属性查询，不依赖函数名）。
func RegisterNodeFile(r Reader, svcName string) (string, bool) {
	n, err := r.GetRegisterNode(svcName)
	if err != nil || n == nil || n.FilePath == "" {
		return "", false
	}
	return n.FilePath, true
}

// RegisterCallSite Register 函数的调用点（source 函数 file + 调用行）。
func RegisterCallSite(r Reader, svcName string) (string, int, bool) {
	reg, err := r.GetRegisterNode(svcName)
	if err != nil || reg == nil {
		return "", 0, false
	}
	call, err := r.GetFirstCallTo(reg.ID)
	if err != nil || call == nil {
		return "", 0, false
	}
	v, ok := call.Metadata["line_num"]
	if !ok {
		return "", 0, false
	}
	line, ok := v.(int)
	if !ok {
		return "", 0, false
	}
	src, err := r.GetSymbol(call.SourceID)
	if err != nil || src == nil || src.FilePath == "" {
		return "", 0, false
	}
	return src.FilePath, line, true
}

// GrpcImpl grpc_impl 边（实现类型 → 服务）的实现节点位置与完整
// canonical ID（R37：流程页按 impl_id 构造方法入口 (Impl).Method）。
// 注册参数为接口（inject.GetXxxService() 返回接口）时，经 implements
// 边（接口 → 实现者）追到具体实现类（go2o 实测：grpc_impl source 是
// 接口名，业务实现才是 Q5 契约要的 impl）。
func GrpcImpl(r Reader, svcID string) (string, string, string, bool) {
	impl, err := r.GetGrpcImplNode(domain.CanonicalID(svcID))
	if err != nil || impl == nil {
		return "", "", "", false
	}
	implID := string(impl.ID)
	name, f, line, kind := impl.Name, impl.FilePath, impl.LineStart, impl.Kind
	if kind == domain.KindInterface || strings.HasSuffix(name, "Server") {
		// 接口 → implements 边 → 实现 struct
		if impl2, ok := ImplementsImpl(r, implID); ok {
			implID = impl2
			if n, err := r.GetSymbol(domain.CanonicalID(impl2)); err == nil && n != nil {
				name, f, line = n.Name, n.FilePath, n.LineStart
			} else {
				name, f, line = "", "", 0
			}
		}
	}
	loc := f
	if line > 0 {
		loc = fmt.Sprintf("%s:%d", f, line)
	}
	return name, implID, loc, true
}

// NodeLoc 节点 name/file_path/line_start/kind。
func NodeLoc(r Reader, id string) (string, string, int, string) {
	n, err := r.GetSymbol(domain.CanonicalID(id))
	if err != nil || n == nil {
		return "", "", 0, ""
	}
	return n.Name, n.FilePath, n.LineStart, string(n.Kind)
}

// ImplementsImpl 接口 → implements 边 → 实现者（SCIP is_implementation：
// 接口指向实现；R37 ast 断言扫描 conf 0.8 兜底）。排除 protoc 生成桩
// （UnimplementedXxxServer——go2o 实测首个命中总是它，业务实现才是
// 契约要的）。
func ImplementsImpl(r Reader, ifaceID string) (string, bool) {
	id, err := r.GetImplementsTarget(domain.CanonicalID(ifaceID))
	if err != nil || id == "" {
		return "", false
	}
	return string(id), true
}
