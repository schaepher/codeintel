package main

import "testing"

// Q247 §2.2：构建期内存上限兜底判定（纯函数，便于单测）。
// 优先级：GOMEMLIMIT（Go 原生已生效，不覆盖）> CODEINTEL_MEMLIMIT
// （显式）> 自动（MemTotal < 4GiB → min(1.5GiB, 55%×MemTotal)）> 不设。
func TestDecideMemLimit(t *testing.T) {
	const gib = 1 << 30
	cases := []struct {
		name     string
		env      map[string]string
		memTotal uint64
		want     int64
		wantSrc  string
	}{
		{
			name:     "GOMEMLIMIT 已设置时不覆盖",
			env:      map[string]string{"GOMEMLIMIT": "2GiB"},
			memTotal: 3 * gib,
			want:     0,
			wantSrc:  memSrcGoEnv,
		},
		{
			name:     "CODEINTEL_MEMLIMIT 显式值优先于自动",
			env:      map[string]string{"CODEINTEL_MEMLIMIT": "2GiB"},
			memTotal: 3 * gib, // 自动本会给 1.5GiB
			want:     2 * gib,
			wantSrc:  memSrcCodeintelEnv,
		},
		{
			name:     "小内存自动兜底（3GiB → 封顶 1.5GiB）",
			env:      map[string]string{},
			memTotal: 3 * gib, // 55% = 1.65GiB > 1.5GiB 上限 → 取 1.5GiB
			want:     3 * gib / 2,
			wantSrc:  memSrcAuto,
		},
		{
			name:     "内存未知（读不到 /proc/meminfo）不设",
			env:      map[string]string{},
			memTotal: 0,
			want:     0,
			wantSrc:  memSrcNone,
		},
		{
			name:     "大内存机器不设",
			env:      map[string]string{},
			memTotal: 16 * gib,
			want:     0,
			wantSrc:  memSrcNone,
		},
		{
			name:     "非法 CODEINTEL_MEMLIMIT → 忽略（不设）",
			env:      map[string]string{"CODEINTEL_MEMLIMIT": "abc"},
			memTotal: 3 * gib,
			want:     0,
			wantSrc:  memSrcInvalid,
		},
		{
			name:     "小内存（0.5GiB）按 55% 取（未触及 1.5GiB 上限）",
			env:      map[string]string{},
			memTotal: 512 << 20,
			want:     int64(512<<20) / 100 * 55,
			wantSrc:  memSrcAuto,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			got, src := decideMemLimit(getenv, c.memTotal)
			if src != c.wantSrc {
				t.Fatalf("source = %s, want %s", src, c.wantSrc)
			}
			if got != c.want {
				t.Fatalf("limit = %d, want %d", got, c.want)
			}
		})
	}
}

// TestParseMemLimit：支持纯字节 / MB(10^6) / MiB(2^20) / GB / GiB 与小数。
func TestParseMemLimit(t *testing.T) {
	cases := map[string]int64{
		"1500MiB": 1500 << 20,
		"2GiB":    2 << 30,
		"2GB":     2000000000,
		"512MB":   512000000,
		"1.5GiB":  1610612736,
		"1048576": 1048576,
		"":        0,
		"abc":     0,
		"-1GiB":   0,
	}
	for in, want := range cases {
		if got := parseMemLimit(in); got != want {
			t.Errorf("parseMemLimit(%q) = %d, want %d", in, got, want)
		}
	}
}
