package cli

// todo24 §2：--continue-segments 的用法校验必须在开库前完成（0/负数/非数字/
// 缺值/超过 128/用错命令 → 非零退出，绝不静默忽略）。

import (
	"context"
	"strings"
	"testing"
)

func runContinueCLI(args ...string) (int, string, string) {
	var code int
	out := captureStdout(func() {
		code = Main(context.Background(), args)
	})
	return code, out, ""
}

func TestContinueSegmentsFlagValidation(t *testing.T) {
	// repo 不存在：如果代码走到开库/解析仓库，会得到 exit=1 与其他错误文本；
	// exit=2 + 参数错误文案证明校验发生在开库之前。
	// 注意：stdout 捕获在 Main 内部完成（错误只走 stderr），用子进程更稳，
	// 这里直接捕获 stderr。
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"zero", []string{"query", "sequence", "Entry", "--repo", "/nonexistent-cont-repo",
			"--code", "--continue-segments", "0"}, "1–128"},
		{"negative", []string{"query", "sequence", "Entry", "--repo", "/nonexistent-cont-repo",
			"--code", "--continue-segments", "-3"}, "1–128"},
		{"nonnumeric", []string{"query", "sequence", "Entry", "--repo", "/nonexistent-cont-repo",
			"--code", "--continue-segments", "abc"}, "需要整数"},
		{"missing", []string{"query", "sequence", "Entry", "--repo", "/nonexistent-cont-repo",
			"--code", "--continue-segments"}, "缺少数值"},
		{"tooLarge", []string{"query", "sequence", "Entry", "--repo", "/nonexistent-cont-repo",
			"--code", "--continue-segments", "129"}, "1–128"},
		{"eqFormTooLarge", []string{"query", "sequence", "Entry", "--repo", "/nonexistent-cont-repo",
			"--code", "--continue-segments=99x"}, "需要整数"},
		{"wrongCommand", []string{"query", "symbol", "Entry", "--repo", "/nonexistent-cont-repo",
			"--continue-segments", "3"}, "只能用于"},
		{"withoutCode", []string{"query", "sequence", "Entry", "--repo", "/nonexistent-cont-repo",
			"--continue-segments", "3"}, "只能用于"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var code int
			stderr := captureStderr(func() {
				out := captureStdout(func() { code = Main(context.Background(), c.args) })
				if strings.TrimSpace(out) != "" {
					t.Errorf("参数错误时 stdout 必须为空，got %q", out)
				}
			})
			if code != 2 {
				t.Errorf("exit=%d，期望 2（用法错误）；stderr=%q", code, stderr)
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("stderr 应含 %q，got %q", c.want, stderr)
			}
			if strings.Contains(stderr, "打开") || strings.Contains(stderr, "repo") && strings.Contains(stderr, "不存在") {
				t.Errorf("参数校验不应走到开库/仓库解析：%q", stderr)
			}
		})
	}
}
