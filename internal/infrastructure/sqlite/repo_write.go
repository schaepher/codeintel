package sqlite

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// SaveBatch 在单个事务中保存节点与边（节点必须先于边插入以满足外键）。
func (r *Repo) SaveBatch(nodes []*domain.CodeEntity, edges []*domain.Fact) error {
	logger := zap.L()
	logger.Debug("enter (Repo).SaveBatch")
	defer logger.Debug("exit (Repo).SaveBatch")
	_, err := r.SaveBatchStats(nodes, edges, nil)
	return err
}

// SaveBatchStats 与 SaveBatch 相同，但返回批次统计（跳过的外键冲突边数），
// 并接受函数字段摘要行（function_field_summary）。
// 端点节点不存在的边（如 Git 追踪到 SCIP 未索引的文件）静默跳过，不中断构建。
func (r *Repo) SaveBatchStats(nodes []*domain.CodeEntity, edges []*domain.Fact,
	summaries []*domain.FunctionFieldSummary, origins ...[]*domain.SummaryOrigin) (*saveBatchResult, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).SaveBatchStats")
	defer logger.Debug("exit (Repo).SaveBatchStats")
	result := &saveBatchResult{}
	var freshRefs map[string]int64 // 构建期端点缓存：提交成功后发布
	allOrigins := []*domain.SummaryOrigin{}
	for _, os := range origins {
		allOrigins = append(allOrigins, os...)
	}
	if len(nodes) == 0 && len(edges) == 0 && len(summaries) == 0 && len(allOrigins) == 0 {
		return result, nil
	}
	tx, err := r.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if len(nodes) > 0 {
		// Q254d 实测：多行批量 INSERT 在本项目**不划算**（节点 ON CONFLICT 子句
		// 很大 → 长语句每 chunk 重新解析/计划，抵消往返节省；A/B 见 §103.4）
		// ——保留预编译 + 逐行 Exec。
		stmt, err := tx.Prepare(insertNodeSQL)
		if err != nil {
			return nil, fmt.Errorf("prepare node insert: %w", err)
		}
		for _, n := range nodes {
			// P1-11：函数内节点的查询靠"ID 以 funcID+"#" 开头"这个不变量
			// 做前缀范围扫（funcid_range.go）——违反就必须报错，不能静默
			// 写进去（否则相关节点在 trace/flows 里消失）。
			if err := validateFuncIDPrefix(n); err != nil {
				stmt.Close()
				return nil, err
			}
			props, err := marshalPropsJSON(n.Properties)
			if err != nil {
				stmt.Close()
				return nil, fmt.Errorf("marshal properties of %s: %w", n.ID, err)
			}
			if _, err := stmt.Exec(string(n.ID), string(n.Kind), n.Name, n.FilePath,
				n.LineStart, n.LineEnd, props); err != nil {
				stmt.Close()
				return nil, fmt.Errorf("insert node %s: %w", n.ID, err)
			}
		}
		stmt.Close()
	}
	if len(edges) > 0 {
		// Q254c：canonical ID → nodes.rowid（整数代理键）批量解析；解析不到的
		// 端点（节点还没落库）走 FailedEdges 延迟重试——与原来"外键冲突"同语义。
		// Q?：构建期正缓存（build_write_cache.go）命中即跳过查询；fresh 只在
		// 事务提交成功后发布。
		refs, fresh, rerr := r.resolveNodeRefsCached(tx, edges)
		if rerr != nil {
			return nil, rerr
		}
		freshRefs = fresh
		stmt, err := tx.Prepare(insertEdgeSQL)
		if err != nil {
			return nil, fmt.Errorf("prepare edge insert: %w", err)
		}
		for _, e := range edges {
			src, okSrc := refs[string(e.SourceID)]
			dst, okDst := refs[string(e.TargetID)]
			if !okSrc || !okDst {
				// Q254c：端点解析不到（节点未落库）→ 延迟重试（与 FK 冲突同路径）
				result.FailedEdges = append(result.FailedEdges, e)
				continue
			}
			meta, err := json.Marshal(e.Metadata)
			if err != nil {
				stmt.Close()
				return nil, fmt.Errorf("marshal metadata: %w", err)
			}
			// todo11-1：edge_key 参与身份（grpc_call = RPC 方法名）
			if _, err := stmt.Exec(src, dst, string(e.Kind), e.Key,
				e.ToolSource, e.Confidence, string(meta)); err != nil {
				if isFKError(err) {
					result.FailedEdges = append(result.FailedEdges, e)
					continue
				}
				stmt.Close()
				return nil, fmt.Errorf("insert edge %s->%s (%s): %w", e.SourceID, e.TargetID, e.Kind, err)
			}
		}
		stmt.Close()
	}
	// P1-11：摘要表的 function_id（canonical）→ function_ref（nodes.id_int）。
	// 同事务内节点先于摘要写入，故同批新节点也能解析到；解析不到的
	// （跨批依赖）走 FailedSummaries/FailedOrigins 延后重试——与原 FK
	// 冲突路径同语义。
	var summaryRefs map[string]int64
	if len(summaries) > 0 || len(allOrigins) > 0 {
		summaryRefs, err = resolveSummaryRefs(tx, summaries, allOrigins)
		if err != nil {
			return nil, err
		}
	}
	if len(summaries) > 0 {
		stmt, err := tx.Prepare(insertSummarySQL)
		if err != nil {
			return nil, fmt.Errorf("prepare summary insert: %w", err)
		}
		for _, s := range summaries {
			ref, ok := summaryRefs[string(s.FunctionID)]
			if !ok {
				result.FailedSummaries = append(result.FailedSummaries, s)
				continue
			}
			if _, err := stmt.Exec(ref, s.AccessKind, s.FieldPath,
				s.InstancePath, s.LineStart, s.CodeSnippet); err != nil {
				if isFKError(err) {
					result.FailedSummaries = append(result.FailedSummaries, s)
					continue
				}
				stmt.Close()
				return nil, fmt.Errorf("insert summary %s %s %s: %w", s.FunctionID, s.AccessKind, s.FieldPath, err)
			}
		}
		stmt.Close()
	}
	if len(allOrigins) > 0 {
		stmt, err := tx.Prepare(`INSERT OR REPLACE INTO summary_origins
			(function_ref, access_kind, field_path, call_line, callee_id)
			VALUES (?, ?, ?, ?, ?)`) // Q215：覆盖旧行（同 Q215 summary）
		if err != nil {
			return nil, fmt.Errorf("prepare origin insert: %w", err)
		}
		for _, o := range allOrigins {

			if o.FunctionID == "" || o.AccessKind == "" || o.FieldPath == "" {
				continue
			}
			ref, ok := summaryRefs[string(o.FunctionID)]
			if !ok {
				result.FailedOrigins = append(result.FailedOrigins, o)
				continue
			}
			if _, err := stmt.Exec(ref, o.AccessKind, o.FieldPath,
				o.CallLine, string(o.CalleeID)); err != nil {
				if isFKError(err) {
					result.FailedOrigins = append(result.FailedOrigins, o)
					continue
				}
				stmt.Close()
				return nil, fmt.Errorf("insert origin %s %s %s: %w", o.FunctionID, o.AccessKind, o.FieldPath, err)
			}
		}
		stmt.Close()
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// 事务成功提交后才发布正向键（回滚时新节点 id_int 未持久化）。
	r.publishBuildWriteCache(freshRefs)
	return result, nil
}

// isFKError 判断是否为外键约束错误（SQLITE_CONSTRAINT_FOREIGNKEY = 787）。
// 驱动无关实现：mattn/go-sqlite3（cgo）的 sqlite3.Error 类型在交叉编译
// CGO_ENABLED=0 时不存在（#227 release 交叉编译暴露），modernc 驱动
// 类型亦不同——统一用 SQLite 官方报错文案匹配（两驱动输出一致）。
func isFKError(err error) bool {
	logger := zap.L()
	logger.Debug("enter isFKError")
	defer logger.Debug("exit isFKError")
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

// SaveNode 保存单个节点（TD.md 4.2 接口）。
func (r *Repo) SaveNode(node *domain.CodeEntity) error {
	logger := zap.L()
	logger.Debug("enter (Repo).SaveNode")
	defer logger.Debug("exit (Repo).SaveNode")
	return r.SaveBatch([]*domain.CodeEntity{node}, nil)
}

// SaveEdges 保存边列表（TD.md 4.2 接口）。
func (r *Repo) SaveEdges(edges []*domain.Fact) error {
	logger := zap.L()
	logger.Debug("enter (Repo).SaveEdges")
	defer logger.Debug("exit (Repo).SaveEdges")
	return r.SaveBatch(nil, edges)
}

// DeleteByFile 删除某个文件的所有节点及其边（级联），用于增量构建。
func (r *Repo) DeleteByFile(filePath string) error {
	logger := zap.L()
	logger.Debug("enter (Repo).DeleteByFile")
	defer logger.Debug("exit (Repo).DeleteByFile")
	_, err := r.Exec("DELETE FROM nodes WHERE file_path = ?", filePath)
	if err != nil {
		return fmt.Errorf("delete nodes of file %s: %w", filePath, err)
	}
	return nil
}

// Counts 返回节点数与边数（构建报告用）。
func (r *Repo) Counts() (nodes, edges int, err error) {
	logger := zap.L()
	logger.Debug("enter (Repo).Counts")
	defer logger.Debug("exit (Repo).Counts")
	if err = r.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&nodes); err != nil {
		return 0, 0, err
	}
	if err = r.QueryRow("SELECT COUNT(*) FROM edges").Scan(&edges); err != nil {
		return 0, 0, err
	}
	return nodes, edges, nil
}

// ResetGraphTables 清空图数据表（DROP + 重建）——全量重建语义
// （orchestrator.FullBuild 用）。比 DELETE 全表快（无逐行 WAL/索引
// 维护）且释放文件空间；build_metadata（构建记录）与未来配置表
// 保留。FK 顺序：先 DROP 子表（edges/function_field_summary）
// 再 DROP 父表（nodes）。
func (r *Repo) ResetGraphTables() error {
	logger := zap.L()
	logger.Debug("enter (Repo).ResetGraphTables")
	defer logger.Debug("exit (Repo).ResetGraphTables")
	tx, err := r.Begin()
	if err != nil {
		return fmt.Errorf("begin reset tx: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS edges",
		"DROP TABLE IF EXISTS function_field_summary",
		"DROP TABLE IF EXISTS nodes",
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("reset %s: %w", stmt, err)
		}
	}

	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("rebuild schema: %w", err)
	}
	return tx.Commit()
}
