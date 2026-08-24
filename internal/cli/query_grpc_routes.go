package cli

// R29 `codeintel query grpc-routes`——服务端 gRPC 路由清单（待办 1
// grpc 部分）：从已有节点发现——grpc_service 节点（markServiceEntry
// 注册点建立）→ RegisterXxxServer 函数（生成代码 .pb.go）→ 注册调用
// 点（calls 入边 line_num）+ grpc_impl 边（实现类型）+ ServiceDesc
// 全量方法（go/parser 提取生成代码——grpc_call 边只含被调用过的方法，
// 服务端定义全集在 ServiceDesc）。输出 JSON 契约（Q5）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// grpcRouteMethod 一个服务方法（ServiceDesc 定义全集）。
type grpcRouteMethod struct {
	Name    string `json:"name"`
	Handler string `json:"handler"`
}

// grpcRouteService 一个 gRPC 服务（服务端路由）。
type grpcRouteService struct {
	Name     string            `json:"name"`      // 服务名（QueryService）
	Impl     string            `json:"impl"`      // 实现类型（grpc_impl 边）
	ImplFile string            `json:"impl_file"` // 实现位置（file:line）
	Register string            `json:"register"`  // 注册调用点（file:line）
	Methods  []grpcRouteMethod `json:"methods"`   // 服务方法全集（ServiceDesc）
}

// grpcRoutesResult 查询结果（Q5 契约）。
type grpcRoutesResult struct {
	Services []grpcRouteService `json:"services"`
}

// grpcRoutes 服务端路由清单：grpc_service 节点 → Register 函数 →
// 调用点/实现/方法。
func grpcRoutes(repo *sqlite.Repo, repoAbs string) (*grpcRoutesResult, error) {
	res := &grpcRoutesResult{Services: []grpcRouteService{}}
	// 1. 全部 grpc_service 节点（服务名）
	rows, err := repo.Query(`SELECT id, json_extract(properties, '$.service_name') FROM nodes WHERE kind = 'grpc_service'`)
	if err != nil {
		return nil, err
	}
	type svcRef struct {
		id   string
		name string
	}
	var svcs []svcRef
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		if name == "" {
			name = strings.TrimPrefix(id, "symbol:")
		}
		svcs = append(svcs, svcRef{id: id, name: name})
	}
	rows.Close()

	for _, sv := range svcs {
		out := grpcRouteService{Name: sv.name, Methods: []grpcRouteMethod{}}
		// 2. Register 函数（签名识别建立——按 registers_service 属性查，
		// R30：不再按函数名 RegisterXxxServer 推导）
		regFile := ""
		if f, ok := registerNodeFile(repo, sv.name); ok {
			regFile = f
		}
		// 3. 注册调用点：Register 函数的 calls 入边 → source 函数 file:line
		if src, line, ok := registerCallSite(repo, sv.name); ok {
			out.Register = fmt.Sprintf("%s:%d", src, line)
		}
		// 4. 实现类型：grpc_impl 边（impl → 服务）
		if impl, implFile, ok := grpcImpl(repo, sv.id); ok {
			out.Impl = impl
			out.ImplFile = implFile
		}
		// 5. 方法全集：ServiceDesc（生成代码文件）
		if regFile != "" {
			if m := serviceDescMethods(filepath.Join(repoAbs, regFile), sv.name); len(m) > 0 {
				out.Methods = m
			}
		}
		res.Services = append(res.Services, out)
	}
	sort.Slice(res.Services, func(i, j int) bool { return res.Services[i].Name < res.Services[j].Name })
	return res, nil
}

// registerNodeFile 按 registers_service 属性查注册函数文件（R30：
// 签名识别建立——属性查询，不依赖函数名）。
func registerNodeFile(repo *sqlite.Repo, svcName string) (string, bool) {
	rows, err := repo.Query(`SELECT file_path FROM nodes WHERE json_extract(properties, '$.registers_service') = ? LIMIT 1`, svcName)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	if rows.Next() {
		var f string
		if err := rows.Scan(&f); err == nil && f != "" {
			return f, true
		}
	}
	return "", false
}

// registerCallSite Register 函数的调用点（source 函数 file + 调用行）。
func registerCallSite(repo *sqlite.Repo, svcName string) (string, int, bool) {
	// Register 函数 id（registers_service 属性——签名识别）
	rows, err := repo.Query(`SELECT id FROM nodes WHERE json_extract(properties, '$.registers_service') = ? LIMIT 1`, svcName)
	if err != nil {
		return "", 0, false
	}
	var regID string
	ok := rows.Next()
	if ok {
		_ = rows.Scan(&regID)
	}
	rows.Close()
	if regID == "" {
		return "", 0, false
	}
	// calls 入边（source 节点 → Register）+ 调用行号
	rows, err = repo.Query(`SELECT source_id, json_extract(metadata, '$.line_num') FROM edges WHERE target_id = ? AND kind = 'calls' LIMIT 1`, regID)
	if err != nil {
		return "", 0, false
	}
	var srcID string
	var line int
	if rows.Next() {
		if err := rows.Scan(&srcID, &line); err != nil {
			rows.Close()
			return "", 0, false
		}
	}
	rows.Close() // 先收完再开内层查询（SQLite 单连接嵌套会死锁）
	if srcID == "" {
		return "", 0, false
	}
	// source 函数文件
	srows, err := repo.Query(`SELECT file_path FROM nodes WHERE id = ?`, srcID)
	if err != nil {
		return "", 0, false
	}
	defer srows.Close()
	if srows.Next() {
		var f string
		if err := srows.Scan(&f); err == nil && f != "" {
			return f, line, true
		}
	}
	return "", 0, false
}

// grpcImpl grpc_impl 边（实现类型 → 服务）的实现节点位置。
// 注册参数为接口（inject.GetXxxService() 返回接口）时，经 implements
// 边（接口 → 实现者）追到具体实现类（go2o 实测：grpc_impl source 是
// 接口名，业务实现才是 Q5 契约要的 impl）。
func grpcImpl(repo *sqlite.Repo, svcID string) (string, string, bool) {
	rows, err := repo.Query(`SELECT source_id FROM edges WHERE target_id = ? AND kind = 'grpc_impl' LIMIT 1`, svcID)
	if err != nil {
		return "", "", false
	}
	var implID string
	if rows.Next() {
		if err := rows.Scan(&implID); err != nil {
			rows.Close()
			return "", "", false
		}
	}
	rows.Close() // 先收完再开内层查询（SQLite 单连接嵌套会死锁）
	if implID == "" {
		return "", "", false
	}
	name, f, line, kind := nodeLoc(repo, implID)
	if kind == string(domain.KindInterface) || strings.HasSuffix(name, "Server") {
		// 接口 → implements 边 → 实现 struct
		impl2, ok := implementsImpl(repo, implID)
		if ok {
			name, f, line, _ = nodeLoc(repo, impl2)
		}
	}
	loc := f
	if line > 0 {
		loc = fmt.Sprintf("%s:%d", f, line)
	}
	return name, loc, true
}

// nodeLoc 节点 name/file_path/line_start/kind。
func nodeLoc(repo *sqlite.Repo, id string) (string, string, int, string) {
	nrows, err := repo.Query(`SELECT name, file_path, line_start, kind FROM nodes WHERE id = ?`, id)
	if err != nil {
		return "", "", 0, ""
	}
	defer nrows.Close()
	if nrows.Next() {
		var name, f, kind string
		var line int
		if err := nrows.Scan(&name, &f, &line, &kind); err == nil {
			return name, f, line, kind
		}
	}
	return "", "", 0, ""
}

// implementsImpl 接口 → implements 边 → 实现者（SCIP is_implementation：
// 接口指向实现）。排除 protoc 生成桩（UnimplementedXxxServer——go2o
// 实测首个命中总是它，业务实现才是契约要的）。
func implementsImpl(repo *sqlite.Repo, ifaceID string) (string, bool) {
	rows, err := repo.Query(`SELECT e.target_id FROM edges e JOIN nodes n ON n.id = e.target_id
		WHERE e.source_id = ? AND e.kind = 'implements' AND n.name NOT LIKE 'Unimplemented%' LIMIT 1`, ifaceID)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	if rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil && id != "" {
			return id, true
		}
	}
	return "", false
}

// cmdGrpcRoutes 实现 `codeintel query grpc-routes [--repo <path>] [--json]`
// ——服务端 gRPC 路由清单（契约化 JSON，Agent 直接解析）。
func cmdGrpcRoutes(repoAbs string, f queryFlags) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	res, err := grpcRoutes(sqlite.NewRepo(db), repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.json {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	for _, s := range res.Services {
		fmt.Printf("[%s] 实现 %s（%s） 注册 %s\n", s.Name, s.Impl, s.ImplFile, s.Register)
		for _, m := range s.Methods {
			fmt.Printf("  %s（%s）\n", m.Name, m.Handler)
		}
	}
	fmt.Printf("\n共 %d 个 gRPC 服务\n", len(res.Services))
	return 0
}
