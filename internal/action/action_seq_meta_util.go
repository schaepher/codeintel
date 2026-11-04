package action

// 时序/timing 与 degrade_stats 数值读取辅助。

import "time"

// millis Duration → 毫秒（保留小数——单次阶段可能远小于 1ms）。
func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// coverageNum degrade_stats 数值键提取（JSON 反序列化为 float64）。
func coverageNum(stats map[string]any, key string) int64 {
	if stats == nil {
		return 0
	}
	switch v := stats[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}
