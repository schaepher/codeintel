package sqlite

// todo22 §4.1：go-micro / native gRPC 的 RPC 转发与注册查询（收口到仓储层——
// 查询端不写 SQL、不做全库发现）。
//
// 数据来源（AST 适配器发射）：
//   - grpc_call 边：RPC 客户端调用点 → grpc_service 节点（edge_key = 方法名）
//     native gRPC：source 是业务调用方函数；go-micro：source 是生成客户端的
//     桩方法（NewRequest/Call 在桩内，服务端续接按桩的转发边恢复）。
//   - grpc_impl 边：实现类型 → grpc_service 节点（服务端注册实参）。
//   - grpc_service 节点属性：service_name（短名）、rpc_service（go-micro 完整
//     身份）、rpc_protocol（grpc|micro）、methods（方法全集）。

import (
	"encoding/json"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

const (
	// rpcForwardsSQL 某函数的 RPC 出站边（按方法名稳定排序）。
	rpcForwardsSQL = `SELECT e.target_id,
		COALESCE(e.edge_key, json_extract(e.metadata, '$.method'), ''),
		COALESCE(json_extract(e.metadata, '$.line_num'), 0),
		COALESCE(json_extract(e.metadata, '$.pos'), 0),
		COALESCE(n.properties, '{}')
		FROM edges_v e JOIN nodes n ON n.id = e.target_id
		WHERE e.source_id = ? AND e.kind = 'grpc_call'
		ORDER BY e.edge_key, e.target_id`
	// rpcServicesSQL 服务节点按协议过滤（kind 部分索引命中；属性过滤在库内完成）。
	rpcServicesSQL = `SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE kind = 'grpc_service'
		AND COALESCE(json_extract(properties, '$.rpc_protocol'), ?) = ?`
	// 完整身份查询：服务端注册身份（rpc_service）。客户端声明身份
	// （rpc_client_service）不参与精确匹配——只有注册身份才能证明"这个服务
	// 在本地有实现"；客户端声明与注册身份不同由调用方按短名兜底时判冲突。
	rpcServicesByFullSQL = `SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE kind = 'grpc_service'
		AND COALESCE(json_extract(properties, '$.rpc_protocol'), ?) = ?
		AND json_extract(properties, '$.rpc_service') = ?`
	rpcImplsSQL = `SELECT source_id FROM edges_v WHERE target_id = ? AND kind = 'grpc_impl' ORDER BY source_id`
)

// GetRPCForwards 某函数的 RPC 出站转发（grpc_call 边 + 服务节点属性）。
// 无出站边返回空切片（正常未命中，不是错误）。
func (r *Repo) GetRPCForwards(source domain.CanonicalID) ([]*domain.RPCForward, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetRPCForwards")
	defer logger.Debug("exit (Repo).GetRPCForwards")
	rows, err := r.Query(rpcForwardsSQL, string(source))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.RPCForward
	for rows.Next() {
		var (
			target, method string
			line, pos      int
			props          string
		)
		if err := rows.Scan(&target, &method, &line, &pos, &props); err != nil {
			return nil, err
		}
		f := &domain.RPCForward{
			SourceID:  source,
			ServiceID: domain.CanonicalID(target),
			Method:    method,
			Line:      line,
			Pos:       pos,
			Protocol:  domain.RPCProtocolGrpc,
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(props), &m); err == nil {
			f.ServiceName, _ = m["service_name"].(string)
			f.FullIdentity, _ = m["rpc_client_service"].(string)
			f.RegistrationIdentity, _ = m["rpc_service"].(string)
			f.HandlerName, _ = m["handler_name"].(string)
			if p, _ := m["rpc_protocol"].(string); p != "" {
				f.Protocol = p
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetRPCServicesByProtocol 指定协议的全部服务节点（kind='grpc_service' 部分
// 索引过滤——不是全库扫描；调用方按身份做精确/弱匹配，不在 SQL 里猜）。
func (r *Repo) GetRPCServicesByProtocol(protocol string) ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetRPCServicesByProtocol")
	defer logger.Debug("exit (Repo).GetRPCServicesByProtocol")
	rows, err := r.Query(rpcServicesSQL, string(domain.RPCProtocolGrpc), protocol)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// GetRPCServicesByFullIdentity 完整服务身份精确查询（rpc_service 表达式索引）。
func (r *Repo) GetRPCServicesByFullIdentity(protocol, full string) ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetRPCServicesByFullIdentity")
	defer logger.Debug("exit (Repo).GetRPCServicesByFullIdentity")
	if strings.TrimSpace(full) == "" {
		return nil, nil
	}
	rows, err := r.Query(rpcServicesByFullSQL, string(domain.RPCProtocolGrpc), protocol, full)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// GetGrpcImplNodes 服务的全部实现类型（grpc_impl 边 source）——与
// GetGrpcImplNode（只取首个）不同：多实现必须全部返回，调用方不得静默取一个。
func (r *Repo) GetGrpcImplNodes(svcID domain.CanonicalID) ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetGrpcImplNodes")
	defer logger.Debug("exit (Repo).GetGrpcImplNodes")
	rows, err := r.Query(rpcImplsSQL, string(svcID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CodeEntity
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			return nil, err
		}
		out = append(out, &domain.CodeEntity{ID: domain.CanonicalID(src)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
