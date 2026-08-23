package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMaybeInstallHook：#234 索引自动更新闭环——post-commit hook 询问
// 安装：拒绝不生成 / 同意生成且可执行 / 幂等跳过 / 用户已有 hook 不覆盖。
func TestMaybeInstallHook(t *testing.T) {
	// 1. 拒绝 → 不生成
	dir := seedGitRepo(t)
	if err := maybeInstallHook(dir, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "post-commit")); !os.IsNotExist(err) {
		t.Error("拒绝时应不生成 hook")
	}

	// 2. 同意 → 生成 + 可执行 + 内容含 update
	dir2 := seedGitRepo(t)
	if err := maybeInstallHook(dir2, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dir2, ".git", "hooks", "post-commit")
	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("hook 未生成: %v", err)
	}
	if !strings.Contains(string(b), "codeintel update") {
		t.Errorf("hook 应含 codeintel update: %s", b)
	}
	fi, err := os.Stat(hook)
	if err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("hook 应可执行，mode=%v", fi)
	}

	// 3. 幂等：再次安装不改变内容
	before, _ := os.ReadFile(hook)
	if err := maybeInstallHook(dir2, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(hook)
	if string(before) != string(after) {
		t.Error("重复安装不应改写 hook")
	}

	// 4. 用户已有 hook（非 codeintel）→ 不覆盖并报错
	dir3 := seedGitRepo(t)
	userHook := filepath.Join(dir3, ".git", "hooks", "post-commit")
	if err := os.WriteFile(userHook, []byte("#!/bin/sh\necho user\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = maybeInstallHook(dir3, func() bool { return true })
	if err == nil || !strings.Contains(err.Error(), "不覆盖") {
		t.Errorf("用户 hook 应报错不覆盖，got %v", err)
	}
	if b, _ := os.ReadFile(userHook); !strings.Contains(string(b), "user") {
		t.Error("用户 hook 被改写")
	}
}
