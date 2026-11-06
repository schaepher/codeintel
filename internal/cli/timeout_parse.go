package cli

// B11e：--timeout / manifest timeout 的整串解析。
//
// 纯整数按秒、其余按 Go duration；两者都必须完整消费输入。整数秒在乘
// time.Second **之前**做范围检查（溢出后仍为正的 duration 不能通过校验）。

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// seqTimeoutMaxSeconds 整数秒上界（floor(MaxInt64 / time.Second)）。
const seqTimeoutMaxSeconds = int64(math.MaxInt64) / int64(time.Second)

// parseTimeoutExact 解析 timeout（纯整数秒或 Go duration）；非法返回错误，
// 不返回 0 兜底（避免"校验失败后用默认值"）。
func parseTimeoutExact(raw string) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("timeout 为空")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return seqTimeoutFromSeconds(n)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("不是整数秒或 Go duration（必须完整消费输入）")
	}
	if d <= 0 {
		return 0, fmt.Errorf("必须为正时长")
	}
	return d, nil
}

// seqTimeoutFromSeconds 整数秒 → duration（先检查范围，再相乘）。
func seqTimeoutFromSeconds(n int64) (time.Duration, error) {
	if n <= 0 {
		return 0, fmt.Errorf("必须为正整数秒")
	}
	if n > seqTimeoutMaxSeconds {
		return 0, fmt.Errorf("超过最大可表示时长（%d 秒）", seqTimeoutMaxSeconds)
	}
	return time.Duration(n) * time.Second, nil
}
