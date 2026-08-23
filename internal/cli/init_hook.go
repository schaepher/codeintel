package cli

// #234 索引自动更新闭环：post-commit hook（提交后自动 codeintel update
// 增量）——「改了代码查询即新」的用户心智。init 结束时询问安装
// （默认不装，不静默改变行为）；VS Code 扩展保存时自动更新见
// vscode-extension/extension.js。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// maybeInstallHook 询问并安装 post-commit hook。ask 返回是否安装
// （非交互环境由调用方传 false）。幂等：已含 codeintel 标记跳过；
// 用户自建 hook 不覆盖并报错。
func maybeInstallHook(abs string, ask func() bool) error {
	logger := zap.L()
	logger.Debug("enter maybeInstallHook", zap.String("abs", abs))
	defer logger.Debug("exit maybeInstallHook")
	hookDir := filepath.Join(abs, ".git", "hooks")
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return nil // 非 git 仓库跳过
	}
	hookPath := filepath.Join(hookDir, "post-commit")
	if b, err := os.ReadFile(hookPath); err == nil {
		s := string(b)
		if strings.Contains(s, "codeintel update") {
			return nil // 已装（幂等）
		}
		if strings.TrimSpace(s) != "" {
			return fmt.Errorf("已存在非 codeintel 的 post-commit hook（%s）——不覆盖，可手动合并", hookPath)
		}
	}
	if !ask() {
		fmt.Println("跳过。索引更新方式：codeintel update（手动）/ 本提示（下次 init）")
		return nil
	}
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`#!/bin/sh
# codeintel 增量更新（%s 自动生成）——commit 后索引自动跟上
if ! command -v codeintel > /dev/null 2>&1; then
  echo "⚠ codeintel 未安装，索引未更新（go install 后下次提交生效）" >&2
  exit 0
fi
codeintel update --repo "%s" > /dev/null 2>&1 || echo "⚠ codeintel update 失败，索引可能过期（查看 .codeintel/codeintel.log）" >&2
`, filepath.Base(abs), abs)
	if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
		return err
	}
	fmt.Printf("已安装 post-commit hook（提交后自动更新索引）→ %s\n", hookPath)
	return nil
}

// isTerminal 判断 stdin 是否交互终端（非 TTY 时跳过询问，不阻塞脚本）。
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
