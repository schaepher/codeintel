package cli

// R60 `codeintel config default` 测试：输出内置模板（默认配置）到
// stdout——Makefile install 首次初始化时重定向写入。

import (
	"strings"
	"testing"
)

// TestCmdConfigDefault：输出与内置模板一致（全部选项 + 默认值 + 注释）。
func TestCmdConfigDefault(t *testing.T) {
	out := captureStdout(func() {
		if code := cmdConfig([]string{"default"}); code != 0 {
			t.Fatalf("cmdConfig default = %d; want 0", code)
		}
	})
	if out != configExample {
		t.Error("config default 输出应与内置模板一致（Makefile install 依赖此行为写入）")
	}
	for _, want := range []string{"# codeintel 全局配置", "agent: auto", "ai:", "domains: auto", "fill: auto", "ask: auto"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应含 %q:\n%s", want, out)
		}
	}
}

// TestCmdConfigHelp：无参数 → 用法说明（不报错）；未知子命令 → 报错。
func TestCmdConfigHelp(t *testing.T) {
	if code := cmdConfig([]string{}); code != 0 {
		t.Error("无参数应显示用法（0）")
	}
	if code := cmdConfig([]string{"--help"}); code != 0 {
		t.Error("--help 应返回 0")
	}
	if code := cmdConfig([]string{"bogus"}); code == 0 {
		t.Error("未知子命令应报错（非 0）")
	}
}
