//go:build integration

package integration

// todo14-14：结构化 argv 直接 execve（无 shell）验证。

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// verifyDirectArgvExec 用进程 API 直接执行 argv[1:]，要求 exit=0 且得到
// 同一语义请求（重复写入 evidence 输出文件后比对规范化 argv）。
func verifyDirectArgvExec(t *testing.T, bin, nonModule string, env []string, argvList []string, argvStr string) {
	t.Helper()
	if len(argvList) < 2 {
		t.Fatalf("argv 太短: %v", argvList)
	}
	direct := exec.Command(bin, argvList[1:]...)
	direct.Dir = nonModule
	direct.Env = env
	if _, err := direct.Output(); err != nil {
		t.Fatalf("直接执行结构化 argv 失败: %v\nargv=%v", err, argvList[1:])
	}
	// --out 已由 argv 指定：取最后一次写入的输出文件
	outFile := ""
	for i, a := range argvList {
		if a == "--out" && i+1 < len(argvList) {
			outFile = argvList[i+1]
		}
	}
	if outFile == "" {
		t.Fatalf("argv 缺少 --out: %v", argvList)
	}
	directOut, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("直接 argv 应写输出文件: %v", err)
	}
	var directRes map[string]any
	if err := json.Unmarshal(directOut, &directRes); err != nil {
		t.Fatalf("直接 argv 输出非 JSON: %v\n%s", err, directOut)
	}
	dmeta, _ := directRes["meta"].(map[string]any)
	did, _ := dmeta["evidence"].(map[string]any)
	if did == nil || strings.Join(interfaceStrings(did["argv"].([]any)), "\x00") != argvStr {
		t.Errorf("直接 argv 应得到同一语义请求: %+v", did)
	}
}
