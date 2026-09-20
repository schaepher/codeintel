package sqlite

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// Q254 Stage 1：库维护——冗余索引清理 / VACUUM 守卫 / WAL 收尾 / 外键开关 /
// 悬挂边清理。全部为**幂等**操作，可在每次构建时安全调用。
//
// 背景（真实业务库 7.46M 边 / 13GB 实测）：
//   - `cmdInit` 原来无条件 VACUUM：而 ResetGraphTables 走 DROP TABLE+重建，
//     建完 freelist≈0 → VACUUM 一个字节也回收不了，却重写整库（13GB ≈ 7 分钟
//     + 需要同等临时空间，磁盘差点爆）。改为**按 freelist 判定**。
//   - edges 上 `idx_edges_source`/`idx_edges_source_kind`/`idx_edges_target`/
//     `idx_edges_confidence` 冗余（EXPLAIN 证据见 docs/field_trace.md §100）：
//     删除（DROP INDEX，无需重建数据）。
//   - 构建期外键校验是每行 2 次父表探测，且悬挂边走 retryFailedFK 全程驻留
//     内存（实测 20.7 万条）→ 构建期关外键 + 末尾一次性清理悬挂边。
//   - WAL 未设上限/未收尾 → 收尾 checkpoint(TRUNCATE) + journal_size_limit。

var (
	// vacuumMinFreeBytes 触发 VACUUM 的 freelist 下限——低于它重写整库不值得。
	// 可用 CODEINTEL_VACUUM_MIN_MB 覆盖（设 0 = 只要 freelist 非空就回收，
	// 用于"删索引后一次性回收"这类明确场景）。
	vacuumMinFreeBytes int64 = 64 << 20
	// vacuumFreeRatio freelist 占库大小比例阈值。
	vacuumFreeRatio = 0.05
	// vacuumDiskHeadroom VACUUM 需 ≈库大小临时空间，留的余量倍数。
	vacuumDiskHeadroom = 1.2
)

// journalSizeLimit WAL 文件上限（checkpoint 后截断目标，见 db.go DSN）。
const journalSizeLimit = 64 << 20

// init 读取 CODEINTEL_VACUUM_MIN_MB（未设或非法则保持默认）。
func init() {
	if v := os.Getenv("CODEINTEL_VACUUM_MIN_MB"); v != "" {
		var mb int64
		if _, err := fmt.Sscanf(v, "%d", &mb); err == nil && mb >= 0 {
			vacuumMinFreeBytes = mb << 20
		}
	}
}

// redundantEdgeIndexes Q254：低价值/冗余的 edges 索引（DROP INDEX 即可，
// 不影响表结构 → 不需要重建库，故不递增 SchemaVersion）。
var redundantEdgeIndexes = []string{
	"idx_edges_source",      // UNIQUE(source_id,target_id,kind) 最左前缀 + 覆盖
	"idx_edges_source_kind", // 同上（kind 是 UNIQUE 索引列）
	"idx_edges_target",      // idx_edges_target_kind(target_id,kind) 最左前缀顶替
	"idx_edges_confidence",  // 无查询按 confidence 过滤（仅 SELECT 列）
}

// dropRedundantEdgeIndexes 删除冗余 edges 索引；返回实际删除的名字（幂等）。
func dropRedundantEdgeIndexes(db *DB) ([]string, error) {
	var dropped []string
	for _, idx := range redundantEdgeIndexes {
		var n int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&n); err != nil {
			return dropped, fmt.Errorf("check index %s: %w", idx, err)
		}
		if n == 0 {
			continue
		}
		if _, err := db.Exec("DROP INDEX " + idx); err != nil {
			return dropped, fmt.Errorf("drop index %s: %w", idx, err)
		}
		dropped = append(dropped, idx)
	}
	return dropped, nil
}

// VacuumPlan VACUUM 判定结果（含判定依据，供日志/构建报告使用）。
type VacuumPlan struct {
	Run           bool
	DBSize        int64
	FreeListBytes int64
	DiskFree      int64
	Reason        string
}

// decideVacuum 纯判定函数（阈值可测）。
func decideVacuum(dbSize, freeList, diskFree int64) VacuumPlan {
	p := VacuumPlan{DBSize: dbSize, FreeListBytes: freeList, DiskFree: diskFree}
	threshold := vacuumMinFreeBytes
	if r := int64(float64(dbSize) * vacuumFreeRatio); r > threshold {
		threshold = r
	}
	switch {
	case freeList <= 0:
		p.Reason = "freelist 为空——VACUUM 无收益（全量构建 DROP+重建后即为此态）"
	case freeList < threshold:
		p.Reason = fmt.Sprintf("freelist %dMB < 阈值 %dMB（库的 %.1f%%）——收益不足",
			freeList>>20, threshold>>20, 100*float64(freeList)/float64(max64(dbSize, 1)))
	case int64(float64(dbSize)*vacuumDiskHeadroom) > diskFree:
		p.Reason = fmt.Sprintf("磁盘余量不足（需 ≈%dMB，实有 %dMB）——跳过以防写满",
			int64(float64(dbSize)*vacuumDiskHeadroom)>>20, diskFree>>20)
	default:
		p.Run = true
		p.Reason = fmt.Sprintf("freelist %dMB ≥ 阈值 %dMB 且磁盘充足", freeList>>20, threshold>>20)
	}
	return p
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// PlanVacuum 只算不执行（诊断/测试用）。
func (db *DB) PlanVacuum() (VacuumPlan, error) {
	var pageSize, pageCount, freelist int64
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return VacuumPlan{}, err
	}
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return VacuumPlan{}, err
	}
	if err := db.QueryRow("PRAGMA freelist_count").Scan(&freelist); err != nil {
		return VacuumPlan{}, err
	}
	return decideVacuum(pageSize*pageCount, pageSize*freelist, diskFreeBytes(db.repoPath)), nil
}

// VacuumIfWorthwhile 按 PlanVacuum 的判定执行 VACUUM（不满足则跳过并记录原因）。
func (db *DB) VacuumIfWorthwhile(reason string) (VacuumPlan, error) {
	plan, err := db.PlanVacuum()
	if err != nil {
		return plan, err
	}
	if !plan.Run {
		zap.L().Info("skip vacuum", zap.String("reason", plan.Reason),
			zap.Int64("db_mb", plan.DBSize>>20), zap.Int64("freelist_mb", plan.FreeListBytes>>20))
		return plan, nil
	}
	start := time.Now()
	if _, err := db.Exec("VACUUM"); err != nil {
		return plan, fmt.Errorf("vacuum: %w", err)
	}
	zap.L().Info("vacuum done", zap.String("trigger", reason), zap.Duration("elapsed", time.Since(start)),
		zap.Int64("before_mb", plan.DBSize>>20), zap.Int64("freelist_mb", plan.FreeListBytes>>20))
	return plan, nil
}

// diskFreeBytes 所在文件系统的可用字节数（判定 VACUUM 是否有空间）。
func diskFreeBytes(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// CheckpointTruncate 收尾 checkpoint：把 WAL 内容合并回主库并截断 WAL 文件
// （真实库实测构建末尾 WAL 涨到 1.33GB）。PASSIVE 失败（有读者）不报错。
func (db *DB) CheckpointTruncate() (int64, error) {
	return db.checkpoint("TRUNCATE")
}

// CheckpointPassive 非阻塞 checkpoint（构建期周期性调用，WAL 小时是廉价空操作）。
func (db *DB) CheckpointPassive() (int64, error) {
	return db.checkpoint("PASSIVE")
}

func (db *DB) checkpoint(mode string) (int64, error) {
	// page_size 必须先读：单连接（SetMaxOpenConns(1)）下，checkpoint 的 rows
	// 未关闭时再开新 Query 会**死锁**（本项目 sqlite 头号坑）。
	var pageSize int
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, err
	}
	rows, err := db.Query("PRAGMA wal_checkpoint(" + mode + ")")
	if err != nil {
		return 0, fmt.Errorf("wal_checkpoint(%s): %w", mode, err)
	}
	var busy, logPages, checkpointed int64
	if rows.Next() {
		if err := rows.Scan(&busy, &logPages, &checkpointed); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan checkpoint: %w", err)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if busy != 0 {
		zap.L().Warn("wal checkpoint busy（另有读者持快照）",
			zap.String("mode", mode), zap.Int64("log_pages", logPages))
	}
	return int64(pageSize) * logPages, nil
}

// SetForeignKeys 构建期开关外键校验（构建期关掉，末尾用 DropDanglingEdges 兜底）。
func (db *DB) SetForeignKeys(on bool) error {
	v := 0
	if on {
		v = 1
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA foreign_keys=%d", v)); err != nil {
		return fmt.Errorf("set foreign_keys=%d: %w", v, err)
	}
	return nil
}

// DropDanglingEdges 删除端点节点不存在的边，返回删除条数。
//
// 语义与 Q254 之前的外键报错路径一致（悬挂边不进图、计入 skipped），但
// 不再需要"失败边驻留内存 + 末尾重试"（实测 20.7 万条）。
func (db *DB) DropDanglingEdges(batch int) (int, error) {
	if batch <= 0 {
		batch = 5000
	}
	total := 0
	for {
		rows, err := db.Query(fmt.Sprintf(
			"SELECT rowid FROM pragma_foreign_key_check('edges') LIMIT %d", batch))
		if err != nil {
			// 老版本 SQLite 无 pragma_foreign_key_check → 回退 set-based NOT IN
			return db.dropDanglingEdgesBySet()
		}
		var ids []string
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return total, fmt.Errorf("scan dangling rowid: %w", err)
			}
			ids = append(ids, fmt.Sprint(id))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		if _, err := db.Exec(
			"DELETE FROM edges WHERE rowid IN (" + strings.Join(ids, ",") + ")"); err != nil {
			return total, fmt.Errorf("delete dangling edges: %w", err)
		}
		total += len(ids)
	}
}

// dropDanglingEdgesBySet 回退路径（无 pragma_foreign_key_check 时）。
func (db *DB) dropDanglingEdgesBySet() (int, error) {
	res, err := db.Exec(`DELETE FROM edges WHERE source_ref NOT IN (SELECT id_int FROM nodes)
		OR target_ref NOT IN (SELECT id_int FROM nodes)`)
	if err != nil {
		return 0, fmt.Errorf("delete dangling edges (set): %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CheckNoDanglingEdges 校验（构建报告/测试用）：返回悬挂边条数。
func (db *DB) CheckNoDanglingEdges() (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM pragma_foreign_key_check('edges')").Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// walSize 当前 WAL 文件字节数（诊断/测试用）。
func (db *DB) walSize() int64 {
	fi, err := os.Stat(db.repoPath + "/.codeintel/codeintel.db-wal")
	if err != nil {
		return 0
	}
	return fi.Size()
}
