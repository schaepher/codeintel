package cli

// T14：--runtime-data 参数形态的开库前拒绝（重复/缺值/后接 flag/空值）。

import (
	"strings"
	"testing"
)

func TestParseQueryFlagsRuntimeDataRejections(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing", []string{"--runtime-data"}, "缺少路径"},
		{"empty", []string{"--runtime-data="}, "路径为空"},
		{"flag-value", []string{"--runtime-data", "--repo", "/x"}, "后接的是 flag"},
		{"duplicate", []string{"--runtime-data", "a.json", "--runtime-data", "b.json"}, "重复指定"},
		{"duplicate-eq", []string{"--runtime-data=a.json", "--runtime-data=b.json"}, "重复指定"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := parseQueryFlags(c.args)
			if f.runtimeDataErr == "" {
				t.Fatalf("应拒绝并给出错误: %+v", f)
			}
			if !strings.Contains(f.runtimeDataErr, c.want) {
				t.Errorf("错误 = %q, want 含 %q", f.runtimeDataErr, c.want)
			}
		})
	}
	// 合法路径：原样记录。
	f := parseQueryFlags([]string{"--runtime-data", "snap.json"})
	if f.runtimeDataErr != "" || f.runtimeData != "snap.json" {
		t.Errorf("合法参数被拒: %+v", f)
	}
}
