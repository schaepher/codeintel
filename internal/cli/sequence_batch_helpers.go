package cli

// sequence-batch 参数/重放辅助（从 sequence_batch.go 拆出）。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// parseBatchTimeout 秒数或 Go duration。
func parseBatchTimeout(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	var n int64
	if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

// seqBatchParams 场景参数 → 稳定文本。
func seqBatchParams(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, k+"="+params[k])
	}
	return strings.Join(out, ",")
}

// seqBatchReplayLine 内容摘要与实际分析参数（重放用）。
func seqBatchReplayLine(fl seqBatchFlow, runtimePath string, depth int) string {
	hash := "none"
	if runtimePath != "" {
		if data, err := os.ReadFile(runtimePath); err == nil {
			sum := sha256.Sum256(data)
			hash = hex.EncodeToString(sum[:])
		} else {
			hash = "unreadable"
		}
	}
	scene := strings.Join(fl.Scene, ",")
	return fmt.Sprintf("> replay: depth=%d continue=%d scene=%q selector=%q runtime=%s sha256=%s",
		depth, fl.ContinueSegments, scene, fl.CombinationSelector, runtimePath, hash)
}
