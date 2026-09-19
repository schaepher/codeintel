package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// 确保 DB 实现仓储接口
var _ domain.CodeRepository = (*Repo)(nil)
var _ domain.BuildMetadataRepository = (*Repo)(nil)

// Repo 实现 CodeRepository / BuildMetadataRepository。
type Repo struct {
	*DB
	relationHops domain.RelationHops // Q197：三类关系跳数上限（0=不限制），默认 4

	// 任务 #165：serve 进程内关系图缓存（cachedRelationGraph）——
	// 单表展开/全量查询复用内存图，避免每次 loadRelationGraph（go2o
	// 530ms）。图对象只读共享（BFS 纯读，Go map 并发读安全），锁只
	// 保护缓存槽本身；键 = build_id + 分析逻辑版本，构建/逻辑变化
	// 自动失效重载。
	graphMu       sync.RWMutex
	graphCacheKey string // 缓存键；空串 = 不缓存（无 build_metadata）
	graphCache    *relationGraph

	// Q252d：全边集邻接表进程内缓存（edge_graph.go）——GetPath 等每次
	// 全表扫边建 map（77-110ms/次）改为按 build_id 复用；锁只保护槽位
	edgeGraphMu    sync.RWMutex
	edgeGraphCache *edgeGraph
}

// SetRelationHops 配置三类关系的跳数上限（--query-max-hops 等，Q197）：
// 传入 0 的类型不限制；未调用时默认 DefaultRelationHops（全部 4 跳）。
func (r *Repo) SetRelationHops(h domain.RelationHops) {
	r.relationHops = h
}

// NewRepo 基于已打开的数据库创建仓储。
func NewRepo(db *DB) *Repo {
	logger := zap.L()
	logger.Debug("enter NewRepo")
	defer logger.Debug("exit NewRepo")
	return &Repo{DB: db, relationHops: DefaultRelationHops}
}

// Q250：多写者属性（`func_id` / `ssa_op` / `type_string` / `origin_kind`）
// 冲突时取**字典序最小**——原先 json_patch 是"最后写者赢"，构建内并发/
// 遍历顺序一变图内容就变（go2o 双跑残余：func_id 47 行、ssa_op+type_string
// 18 行——同一 #实际名 节点被 alloc 与 load 两个发射点写入不同值）。
// 其余属性仍按 json_patch 合并（"新写者赢"语义不变）。键不存在时原样
// 透传（不引入 JSON null）。
var nodeDeterministicPropKeys = []string{"origin_kind", "ssa_op", "type_string", "func_id"}

// buildInsertNodeSQL 生成 nodes UPSERT：properties 用 json_patch 合并，但
// nodeDeterministicPropKeys 中的键取 min(旧行, 新行)。
func buildInsertNodeSQL() string {
	props := "excluded.properties"
	for _, k := range nodeDeterministicPropKeys {
		path := "'$." + k + "'"
		props = fmt.Sprintf(`CASE WHEN json_extract(excluded.properties, %[1]s) IS NULL THEN %[2]s
             ELSE json_set(%[2]s, %[1]s,
                 CASE WHEN json_extract(properties, %[1]s) IS NOT NULL
                       AND json_extract(properties, %[1]s) < json_extract(excluded.properties, %[1]s)
                      THEN json_extract(properties, %[1]s)
                      ELSE json_extract(excluded.properties, %[1]s) END) END`,
			path, props)
	}
	return `
INSERT INTO nodes (id, kind, name, file_path, line_start, line_end, properties)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    -- kind 取"更具体者"（parameter/receiver/... 优先于泛化的 ssa_value；
    -- 同具体度取字典序最小），否则两个 emitter 的插入顺序决定 kind
    -- （go2o 双跑残余 108 行：同一 #param.x 一次是 parameter 一次是 ssa_value）
    kind = CASE
        WHEN kind = 'ssa_value' AND excluded.kind <> 'ssa_value' THEN excluded.kind
        WHEN excluded.kind = 'ssa_value' THEN kind
        WHEN excluded.kind < kind THEN excluded.kind
        ELSE kind END,
    -- 同一 ID 可能被多个发射点写入不同位置（如实例路径节点在两次访问处
    -- 行号不同）——列取确定赢家（非空优先、更小者优先），否则"首个写者赢"
    file_path = CASE
        WHEN file_path IS NULL OR file_path = '' THEN excluded.file_path
        WHEN excluded.file_path IS NULL OR excluded.file_path = '' THEN file_path
        WHEN excluded.file_path < file_path THEN excluded.file_path
        ELSE file_path END,
    line_start = CASE
        WHEN line_start IS NULL THEN excluded.line_start
        WHEN excluded.line_start IS NULL THEN line_start
        WHEN excluded.line_start < line_start THEN excluded.line_start
        ELSE line_start END,
    line_end = CASE
        WHEN line_end IS NULL THEN excluded.line_end
        WHEN excluded.line_end IS NULL THEN line_end
        WHEN excluded.line_end < line_end THEN excluded.line_end
        ELSE line_end END,
    properties = json_patch(COALESCE(properties, '{}'), ` + props + `)`
}

var insertNodeSQL = buildInsertNodeSQL()

// R69：count 累加（同义边合并保留真实调用次数——每次插入 +1，
// 与置信度无关）；confidence/tool/metadata 仍只在高置信度时覆盖。
const insertEdgeSQL = `
INSERT INTO edges (source_id, target_id, kind, tool_source, confidence, metadata, count)
VALUES (?, ?, ?, ?, ?, ?, 1)
ON CONFLICT(source_id, target_id, kind) DO UPDATE SET
    count = edges.count + 1,
    confidence = CASE WHEN excluded.confidence > edges.confidence THEN excluded.confidence ELSE edges.confidence END,
    tool_source = CASE WHEN excluded.confidence > edges.confidence THEN excluded.tool_source ELSE edges.tool_source END,
    metadata = CASE WHEN excluded.confidence > edges.confidence THEN excluded.metadata ELSE edges.metadata END`

// insertSummarySQL Q215/Q250：REPLACE 覆盖（原 OR IGNORE——UNIQUE 冲突
// 保留旧行，函数修改后行号/代码片段陈旧）。**确定性不在此层做**：同一
// (function_id, access_kind, field_path) 在一次分析内的多个候选（同一字段
// 路径多次访问）由发射端 `emitSummaryRows` 按确定规则选赢家（Q250：最早
// 行号，同行取 instance_path 字典序最小）——若在这里改成"取最早行号赢"
// 会破坏 Q215（增量重建时行号下移的新分析反而被旧行挡住）。
// 行残留（函数删除）由 FK ON DELETE CASCADE 保证。
const insertSummarySQL = `
INSERT OR REPLACE INTO function_field_summary
    (function_id, access_kind, field_path, instance_path, line_start, code_snippet)
VALUES (?, ?, ?, ?, ?, ?)`

// saveBatchResult 记录批次写入的统计信息。
type saveBatchResult struct {
	// SkippedEdges 因外键冲突（端点节点不存在）被跳过的边数。
	// 注：FK 失败先进入 Failed*（构建尾部重试），重试后仍失败才计入。
	SkippedEdges int
	// FailedEdges/FailedSummaries/FailedOrigins FK 冲突项（端点节点尚未
	// 落库——并发构建跨批依赖）→ 调用方收集后于全部节点落库后重试
	// （P2：原实现静默跳过导致非确定性丢边，go2o 三次重建 156217/
	// 156214/156217）。
	FailedEdges     []*domain.Fact
	FailedSummaries []*domain.FunctionFieldSummary
	FailedOrigins   []*domain.SummaryOrigin
}

// Save 保存构建元数据。
func (r *Repo) Save(meta *domain.BuildMeta) error {
	logger := zap.L()
	logger.Debug("enter (Repo).Save")
	defer logger.Debug("exit (Repo).Save")
	dispatchJSON := "[]"
	if len(meta.DispatchPkgs) > 0 {
		if b, err := json.Marshal(meta.DispatchPkgs); err == nil {
			dispatchJSON = string(b)
		}
	}
	_, err := r.Exec(`INSERT OR REPLACE INTO build_metadata
		(build_id, commit_sha, tool_name, status, duration_ms, error_message, nodes_count, edges_count, degrade_stats, dispatch_pkgs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		meta.BuildID, meta.CommitSHA, meta.ToolName, meta.Status, meta.DurationMs, meta.ErrorMsg,
		meta.Nodes, meta.Edges, meta.DegradeStats, dispatchJSON)
	return err
}

// GetLatest 获取最近一次构建元数据。
func (r *Repo) GetLatest() (*domain.BuildMeta, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetLatest")
	defer logger.Debug("exit (Repo).GetLatest")
	m := &domain.BuildMeta{}
	// timestamp 为秒级：同一秒内多次构建须按写入顺序取最新（rowid 递增）
	var dpJSON string
	err := r.QueryRow(`SELECT build_id, commit_sha, tool_name, status, duration_ms, error_message,
		COALESCE(nodes_count, 0), COALESCE(edges_count, 0), COALESCE(degrade_stats, ''),
		COALESCE(dispatch_pkgs, '[]')
		FROM build_metadata ORDER BY timestamp DESC, rowid DESC LIMIT 1`).
		Scan(&m.BuildID, &m.CommitSHA, &m.ToolName, &m.Status, &m.DurationMs, &m.ErrorMsg,
			&m.Nodes, &m.Edges, &m.DegradeStats, &dpJSON)
	if err == nil && len(dpJSON) > 2 {
		if jerr := json.Unmarshal([]byte(dpJSON), &m.DispatchPkgs); jerr != nil {
			m.DispatchPkgs = nil
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}
