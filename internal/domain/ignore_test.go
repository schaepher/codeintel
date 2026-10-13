package domain

import "testing"

// TestIsIgnoredPath：忽略目录匹配（相等/子目录/归一/不误伤前缀相似目录）。
func TestIsIgnoredPath(t *testing.T) {
	cases := []struct {
		rel    string
		ignore []string
		want   bool
	}{
		{"vendor/foo/x.go", []string{"vendor"}, true},
		{"vendor", []string{"vendor"}, true},
		{"third_party/gen/a.go", []string{"third_party/gen"}, true},
		{"third_party/genx/a.go", []string{"third_party/gen"}, false},
		{"pkg/vendor/a.go", []string{"vendor"}, false},
		{"./vendor/a.go", []string{"./vendor/"}, true},
		{"a.go", []string{"vendor"}, false},
		{"a.go", nil, false},
		{"vendor/x.go", []string{""}, false},
	}
	for _, c := range cases {
		if got := IsIgnoredPath(c.rel, c.ignore); got != c.want {
			t.Errorf("IsIgnoredPath(%q, %v) = %v; want %v", c.rel, c.ignore, got, c.want)
		}
	}
}
