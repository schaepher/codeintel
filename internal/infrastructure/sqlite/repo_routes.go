package sqlite

// R92：grpc/http 路由查询窄方法（action 层 Reader 扩展——原 cli 直
// 接 repo.Query 的 SQL 收口到仓储层）：grpc_service 节点、Register
// 函数（registers_service 属性）、注册调用边、grpc_impl 边、implements
// 边、http_route 节点。

import (
	"database/sql"
	"errors"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetGrpcServices kind='grpc_service' 节点全量（含 properties——
// service_name/methods 属性）。
func (r *Repo) GetGrpcServices() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetGrpcServices")
	defer logger.Debug("exit (Repo).GetGrpcServices")
	rows, err := r.Query(`SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE kind = 'grpc_service'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// GetRegisterNode registers_service 属性 = svcName 的注册函数节点
// （R30 签名识别建立；无则 nil）。
func (r *Repo) GetRegisterNode(svcName string) (*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetRegisterNode")
	defer logger.Debug("exit (Repo).GetRegisterNode")
	n, err := scanNode(r.QueryRow(`SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE json_extract(properties, '$.registers_service') = ? LIMIT 1`, svcName))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	return n, err
}

// GetFirstCallTo target 首条 calls 入边（source + 调用行号；无则 nil）。
func (r *Repo) GetFirstCallTo(targetID domain.CanonicalID) (*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetFirstCallTo")
	defer logger.Debug("exit (Repo).GetFirstCallTo")
	rows, err := r.Query(`SELECT source_id, json_extract(metadata, '$.line_num')
		FROM edges WHERE target_id = ? AND kind = 'calls' LIMIT 1`, string(targetID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var srcID string
	var ln sql.NullInt64
	if err := rows.Scan(&srcID, &ln); err != nil {
		return nil, err
	}
	f := &domain.Fact{SourceID: domain.CanonicalID(srcID), TargetID: targetID,
		Kind: domain.FactCalls, Metadata: map[string]any{}}
	if ln.Valid {
		f.Metadata["line_num"] = int(ln.Int64)
	}
	return f, rows.Err()
}

// GetGrpcImplNode grpc_impl 边 source（实现类型/接口）节点；无则 nil。
func (r *Repo) GetGrpcImplNode(svcID domain.CanonicalID) (*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetGrpcImplNode")
	defer logger.Debug("exit (Repo).GetGrpcImplNode")
	var src string
	err := r.QueryRow(`SELECT source_id FROM edges WHERE target_id = ? AND kind = 'grpc_impl' LIMIT 1`,
		string(svcID)).Scan(&src)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.GetSymbol(domain.CanonicalID(src))
}

// GetImplementsTarget implements 边（接口 → 实现者；SCIP is_implementation）
// ——排除 protoc 生成桩（UnimplementedXxxServer）；无则空 ID。
func (r *Repo) GetImplementsTarget(ifaceID domain.CanonicalID) (domain.CanonicalID, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetImplementsTarget")
	defer logger.Debug("exit (Repo).GetImplementsTarget")
	var id string
	err := r.QueryRow(`SELECT e.target_id FROM edges e JOIN nodes n ON n.id = e.target_id
		WHERE e.source_id = ? AND e.kind = 'implements' AND n.name NOT LIKE 'Unimplemented%' LIMIT 1`,
		string(ifaceID)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return domain.CanonicalID(id), err
}

// GetCLICommandNodes kind='cli_command' 节点全量（R100 迁移收尾——
// cliRoutes 裸 SQL 收口；properties 含 cli_name/cli_usage/cli_action/
// cli_parent/register）。
func (r *Repo) GetCLICommandNodes() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetCLICommandNodes")
	defer logger.Debug("exit (Repo).GetCLICommandNodes")
	rows, err := r.Query(`SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE kind = 'cli_command'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// GetPbServerInterfaces 带 pb_servers 属性的接口节点（R100 迁移收尾——
// grpcComposites 裸 SQL 收口）。
func (r *Repo) GetPbServerInterfaces() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetPbServerInterfaces")
	defer logger.Debug("exit (Repo).GetPbServerInterfaces")
	rows, err := r.Query(`SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE kind = 'interface' AND json_extract(properties, '$.pb_servers') IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// GetHTTPRouteNodes kind='http_route' 节点全量（含 properties——
// method/path/handler/handler_id/resolver/register）。
func (r *Repo) GetHTTPRouteNodes() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetHTTPRouteNodes")
	defer logger.Debug("exit (Repo).GetHTTPRouteNodes")
	rows, err := r.Query(`SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE kind = 'http_route'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}
