package main

// Q247 §2.2：构建期内存上限兜底——小内存机器上把 Go 的 GC 目标钉住，
// 用"更多 GC、更少换页"换不 OOM（docs/design-q247.md）。
//
// 优先级（高→低）：
//  1. GOMEMLIMIT：Go 运行时原生支持且**启动时已生效**——我们绝不覆盖
//  2. CODEINTEL_MEMLIMIT：本工具的显式配置
//  3. 自动：MemTotal < 4GiB 时 min(1.5GiB, 55%×MemTotal)
//  4. 不设（大内存机器 / 读不到 MemTotal）
//
// 判定与解析抽成纯函数（decideMemLimit/parseMemLimit）便于单测；副作用
// 只在 applyMemLimit 里（debug.SetMemoryLimit + 日志）。

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// 判定来源（日志与测试用）。
const (
	memSrcGoEnv        = "gomemlimit-env"   // GOMEMLIMIT 已生效，不覆盖
	memSrcCodeintelEnv = "codeintel-env"    // CODEINTEL_MEMLIMIT 显式值
	memSrcAuto         = "auto-small-ram"   // 小内存自动兜底
	memSrcInvalid      = "invalid-override" // CODEINTEL_MEMLIMIT 非法 → 忽略
	memSrcNone         = "none"             // 不设
)

const (
	autoLimitThreshold = 4 << 30 // MemTotal < 4GiB 才启用自动兜底
	autoLimitCap       = 3 << 29 // 自动上限 1.5GiB
	autoLimitPercent   = 55      // 自动取 MemTotal 的 55%
)

// decideMemLimit 计算构建期内存上限（0 = 不设）与来源。
func decideMemLimit(getenv func(string) string, memTotal uint64) (int64, string) {
	if v := strings.TrimSpace(getenv("GOMEMLIMIT")); v != "" {
		return 0, memSrcGoEnv // Go 运行时已按该值生效，不覆盖
	}
	if v := strings.TrimSpace(getenv("CODEINTEL_MEMLIMIT")); v != "" {
		n := parseMemLimit(v)
		if n <= 0 {
			return 0, memSrcInvalid
		}
		return n, memSrcCodeintelEnv
	}
	if memTotal > 0 && memTotal < autoLimitThreshold {
		limit := int64(memTotal / 100 * autoLimitPercent)
		if limit > autoLimitCap {
			limit = autoLimitCap
		}
		return limit, memSrcAuto
	}
	return 0, memSrcNone
}

// parseMemLimit 解析内存字符串：纯字节 / B / KB(10^3) / MB / GB /
// KiB(2^10) / MiB / GiB，大小写不敏感，支持小数（如 "1.5GiB"）。
// 非法（含负数、空串）返回 0。
func parseMemLimit(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mult := int64(1)
	upper := strings.ToUpper(s)
	for _, suffix := range []struct {
		suffix string
		mult   int64
	}{
		{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"GB", 1000 * 1000 * 1000}, {"MB", 1000 * 1000}, {"KB", 1000},
		{"B", 1},
	} {
		if strings.HasSuffix(upper, suffix.suffix) {
			mult = suffix.mult
			s = strings.TrimSpace(s[:len(s)-len(suffix.suffix)])
			break
		}
	}
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int64(f * float64(mult))
}

// readMemTotal 读 /proc/meminfo 的 MemTotal（字节）；读不到返回 0
// （非 Linux / 受限环境——不设上限比瞎设安全）。
func readMemTotal() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 1 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// applyMemLimit 应用内存上限（构建类命令用）。返回生效值（0 = 未设），
// 便于测试与调用方日志。
func applyMemLimit(logger *zap.Logger) int64 {
	limit, src := decideMemLimit(os.Getenv, readMemTotal())
	if limit <= 0 {
		if src == memSrcInvalid {
			logger.Warn("CODEINTEL_MEMLIMIT 非法（示例 1500MiB / 2GiB / 纯字节）——已忽略，不设内存上限")
			return 0
		}
		return 0
	}
	debug.SetMemoryLimit(limit)
	mb := limit >> 20
	// 双通道输出：zap 在 logging.Setup 之前是 noop（本函数在 main 早期
	// 调用），直接 stderr 才能让用户看到生效值
	if src == memSrcAuto {
		logger.Info("构建期内存上限（小内存兜底）",
			zap.Int64("limit_mb", mb), zap.Uint64("mem_total_mb", readMemTotal()>>20))
		fmt.Fprintf(os.Stderr, "[index] 内存上限 %dMB（小内存兜底，CODEINTEL_MEMLIMIT 可覆盖）\n", mb)
	} else {
		logger.Info("构建期内存上限", zap.Int64("limit_mb", mb), zap.String("source", src))
		fmt.Fprintf(os.Stderr, "[index] 内存上限 %dMB（CODEINTEL_MEMLIMIT）\n", mb)
	}
	return limit
}
