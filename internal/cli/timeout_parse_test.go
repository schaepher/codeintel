package cli

// todo34 B11e：timeout 整串解析 + 整数秒范围检查（乘 time.Second 之前）。

import (
	"math"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestB11eTimeoutIntegerBoundsPositive(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"1", time.Second},
		{"30", 30 * time.Second},
		{strconv.FormatInt(seqTimeoutMaxSeconds, 10), time.Duration(seqTimeoutMaxSeconds) * time.Second},
		{"30s", 30 * time.Second},
		{"1h30m", 90 * time.Minute},
		{strconv.FormatInt(math.MaxInt64, 10) + "ns", time.Duration(math.MaxInt64)},
	}
	for _, c := range cases {
		got, err := parseTimeoutExact(c.raw)
		if err != nil {
			t.Fatalf("parseTimeoutExact(%q) err = %v（want %s）", c.raw, err, c.want)
		}
		if got != c.want {
			t.Fatalf("parseTimeoutExact(%q) = %s（want %s，原值生效）", c.raw, got, c.want)
		}
	}
}

func TestB11eTimeoutIntegerBoundsNegative(t *testing.T) {
	cases := []string{
		strconv.FormatInt(seqTimeoutMaxSeconds+1, 10), // 上限加 1
		"20000000000",              // 溢出后仍为正的整数秒
		"0",                        // 零
		"-5",                       // 负值
		"999999999999999999999999", // 超过 int64
		"3600oops",                 // 未完整消费
		"0s",                       // 零时长
		"-1s",                      // 负时长
		"9223372036854775808ns",    // Go duration 溢出
		"1d",                       // 非法单位
		"",                         // 空
	}
	for _, raw := range cases {
		got, err := parseTimeoutExact(raw)
		if err == nil {
			t.Fatalf("parseTimeoutExact(%q) = %s（want 拒绝）", raw, got)
		}
	}
}

// 单流程与 batch 共用同一解析契约：batch 的非法输入在开库/建目录前拒绝。
func TestB11eTimeoutPreflightIsolation(t *testing.T) {
	dir := t.TempDir()
	base := t.TempDir()
	manifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:x:Submit","timeout":"20000000000"}],"formats":["markdown"]}`
	mpath := dir + "/flows-overflow.json"
	if err := os.WriteFile(mpath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := base + "/overflow-export"
	code := cmdSequenceBatch([]string{"--manifest", mpath, "--out", outDir, "--json"})
	if code != 1 {
		t.Fatalf("非法 timeout exit = %d（want 1）", code)
	}
	if _, err := os.Stat(outDir); err == nil {
		t.Fatalf("非法 timeout 不应创建输出目录")
	}
}
