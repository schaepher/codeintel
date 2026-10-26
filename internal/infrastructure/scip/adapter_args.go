package scip

// scip-go 命令行参数与版本探测（P1-08/P1-12：全量构建的产物缓存键需要
// scip-go 版本；增量构建把变更包 patterns 追加为位置参数）。

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// indexArgs 构造 scip-go index 参数（P1-12：additional 为 package-patterns）。
func indexArgs(indexPath string, additional []string) []string {
	args := []string{"index", "-o", indexPath, "-q", "--skip-tests"}
	args = append(args, additional...)
	return args
}

// scipGoVersion 探测 scip-go 版本（`--version` 输出如 "0.2.7"）。
func scipGoVersion(ctx context.Context, bin string) (string, error) {
	verCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(verCtx, bin, "--version").Output()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", fmt.Errorf("empty scip-go version output")
	}
	return v, nil
}
