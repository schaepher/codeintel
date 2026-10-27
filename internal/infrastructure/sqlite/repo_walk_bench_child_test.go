package sqlite

// todo22-14.4-B：资源基准的**子进程**——每次测量都用独立进程执行一次
// 查询，避免同进程内 SQLite 页缓存/Go 堆复用污染 RSS 与临时存储峰值。
//
// 由父测试（TestWalkEdgesResourceBench）以
// `<test binary> -test.run=^TestWalkBenchChild$` 拉起，参数走环境变量；
// 结果写回 JSON 文件。被测查询复用生产的 walkDirectQuery /
// walkRecursiveQuery（materialized=false 即物化前的原递归实现）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

const (
	benchEnableEnv   = "CODEINTEL_WALK_BENCH"
	benchChildEnv    = "CODEINTEL_WALK_BENCH_CHILD"
	benchOutEnv      = "CODEINTEL_WALK_BENCH_OUT"
	benchRepsEnv     = "CODEINTEL_WALK_BENCH_REPS"
	benchChildDBEnv  = "CODEINTEL_WALK_BENCH_DB"
	benchChildNEnv   = "CODEINTEL_WALK_BENCH_N"
	benchChildEnvDS  = "CODEINTEL_WALK_BENCH_DEPTH"
	benchChildVarEnv = "CODEINTEL_WALK_BENCH_VARIANT"
	benchChildSmpEnv = "CODEINTEL_WALK_BENCH_SAMPLE"
	benchChildResEnv = "CODEINTEL_WALK_BENCH_RESULT"
	benchChildPIDEnv = "CODEINTEL_WALK_BENCH_PID"
)

const (
	benchVariantRecursive    = "recursive"
	benchVariantMaterialized = "materialized"
	benchVariantDirectOneHop = "direct_one_hop"
)

// benchAnchor 固定锚点（N0000），方向 callees，minConfidence=0.8。
const (
	benchAnchor        = "symbol:go:example.com/materialized:N0000"
	benchMinConfidence = 0.8
)

// benchVariantsFor 返回某深度需要测量的变体：depth<=1 额外记录生产的
// 直接邻接查询（第三组单独记录，不冒充物化组）；多跳只对照原递归与物化。
func benchVariantsFor(depth int) []string {
	if depth <= 1 {
		return []string{benchVariantRecursive, benchVariantMaterialized, benchVariantDirectOneHop}
	}
	return []string{benchVariantRecursive, benchVariantMaterialized}
}

// benchChildQuery 变体 → (SQL, 参数)。方向固定 callees：锚点是 source。
func benchChildQuery(variant string, depth int) (string, []any, error) {
	switch variant {
	case benchVariantRecursive:
		return walkRecursiveQuery("source_id", "tgt", false),
			[]any{benchAnchor, benchMinConfidence, depth, benchMinConfidence}, nil
	case benchVariantMaterialized:
		return walkRecursiveQuery("source_id", "tgt", true),
			[]any{benchAnchor, benchMinConfidence, depth, benchMinConfidence}, nil
	case benchVariantDirectOneHop:
		if depth != 1 {
			return "", nil, fmt.Errorf("direct_one_hop 只对 depth=1 有意义，got %d", depth)
		}
		return walkDirectQuery("source_id"), []any{benchAnchor, benchMinConfidence}, nil
	}
	return "", nil, fmt.Errorf("未知变体 %q", variant)
}

// benchFactsDigest 结果的规范化摘要：按六列（metadata 归一化为 json 文本，
// Go map 序列化键有序）排序后取 SHA-256。摘要相等 ⇔ 逐项一致。
func benchFactsDigest(facts []*domain.Fact) string {
	lines := make([]string, 0, len(facts))
	for _, f := range facts {
		meta := "{}"
		if f.Metadata != nil {
			if b, err := json.Marshal(f.Metadata); err == nil {
				meta = string(b)
			}
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%s|%s|%.3f|%s",
			f.SourceID, f.TargetID, f.Kind, f.ToolSource, f.Confidence, meta))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// TestWalkBenchChild 子进程入口：一次只跑一个 (N, depth, variant, sample)。
func TestWalkBenchChild(t *testing.T) {
	if os.Getenv(benchChildEnv) != "1" {
		t.Skip("资源基准子进程专用（父测试 CODEINTEL_WALK_BENCH=1 时拉起）")
	}
	dbDir := os.Getenv(benchChildDBEnv)
	resPath := os.Getenv(benchChildResEnv)
	n, _ := strconv.Atoi(os.Getenv(benchChildNEnv))
	depth, _ := strconv.Atoi(os.Getenv(benchChildEnvDS))
	sample, _ := strconv.Atoi(os.Getenv(benchChildSmpEnv))
	variant := os.Getenv(benchChildVarEnv)
	if dbDir == "" || resPath == "" || variant == "" {
		t.Fatalf("子进程环境不完整: db=%q result=%q variant=%q", dbDir, resPath, variant)
	}
	// 父进程按 pid 文件定位被测进程（/usr/bin/time 包装时 argv 进程不是它）。
	if pidPath := os.Getenv(benchChildPIDEnv); pidPath != "" {
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}

	res := &benchChildResult{N: n, Depth: depth, Variant: variant, Sample: sample}
	writeResult := func() {
		data, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		if err := os.WriteFile(resPath, data, 0o644); err != nil {
			t.Fatalf("write result: %v", err)
		}
	}

	openStart := time.Now()
	db, err := Open(dbDir)
	if err != nil {
		res.Error = "open: " + err.Error()
		writeResult()
		t.Fatalf("%s", res.Error)
	}
	res.OpenMS = benchMS(openStart)
	res.SQLiteVer, res.TempStore, res.CacheSize = benchPragmas(t, db)
	res.RSSBefore = readProcStatusBytes("VmRSS")

	sql, args, err := benchChildQuery(variant, depth)
	if err != nil {
		res.Error = err.Error()
		writeResult()
		db.Close()
		t.Fatalf("%s", res.Error)
	}
	// 查询边界：QueryContext → 全部 Rows.Next/Scan 完成 → Close（todo22 要求
	// "比较的查询边界必须包含全部 Rows.Next/Scan"）。
	queryStart := time.Now()
	res.QueryStartNS = queryStart.UnixNano()
	rows, err := db.QueryContext(context.Background(), sql, args...)
	if err != nil {
		res.Error = "query: " + err.Error()
		res.QueryMS = benchMS(queryStart)
		writeResult()
		db.Close()
		t.Fatalf("%s", res.Error)
	}
	facts, scanErr := scanFacts(rows)
	rows.Close()
	res.QueryEndNS = time.Now().UnixNano()
	res.QueryMS = benchMS(queryStart)
	if scanErr != nil {
		res.Error = "scan: " + scanErr.Error()
		writeResult()
		db.Close()
		t.Fatalf("%s", res.Error)
	}
	res.ResultEdges = len(facts)
	res.ResultDigest = benchFactsDigest(facts)
	res.VMHWMBytes = readProcStatusBytes("VmHWM")

	closeStart := time.Now()
	if err := db.Close(); err != nil {
		res.Error = "close: " + err.Error()
		writeResult()
		t.Fatalf("%s", res.Error)
	}
	res.CloseMS = benchMS(closeStart)
	writeResult()
}

// benchPragmas 记录查询实际生效的版本与连接级 pragma（temp_store/cache_size
// 决定临时表落盘还是内存，必须随样本一起报告）。
func benchPragmas(t *testing.T, db *DB) (sqliteVer string, tempStore, cacheSize int) {
	t.Helper()
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&sqliteVer); err != nil {
		sqliteVer = "unknown"
	}
	_ = db.QueryRow("PRAGMA temp_store").Scan(&tempStore)
	_ = db.QueryRow("PRAGMA cache_size").Scan(&cacheSize)
	return sqliteVer, tempStore, cacheSize
}

// readProcStatusBytes 读 /proc/self/status 的字段（字节）；不可用返回 0。
func readProcStatusBytes(field string) int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, field+":") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, field+":"))
		if len(fields) == 0 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024 // VmRSS/VmHWM 单位 kB
	}
	return 0
}

func benchMS(since time.Time) float64 {
	return float64(time.Since(since).Microseconds()) / 1000
}
