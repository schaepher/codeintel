package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestStartCPUProfileDecodable：启动→烧 CPU→停止后，profile 文件必须是
// 可被解析的 pprof 数据（gzip 头 + `go tool pprof -raw` 退出码 0）。
// 回归点：旧实现在停止时重新 os.Create 同一路径，截断后 profile 损坏。
func TestStartCPUProfileDecodable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cpu.prof")
	t.Setenv("CODEINTEL_CPU_PROFILE", path)

	stop, active := startCPUProfile("init")
	if !active {
		t.Fatalf("startCPUProfile(init) active = false, want true")
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	x := 0
	for time.Now().Before(deadline) {
		x++
	}
	_ = x
	stop()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("profile 未生成: %v", err)
	}
	head := data
	if len(head) > 2 {
		head = head[:2]
	}
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		t.Fatalf("profile 不是 gzip（截断/损坏）：len=%d head=%q", len(data), head)
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Logf("未找到 go，跳过 go tool pprof 解码检查")
		return
	}
	out, err := exec.Command(goTool, "tool", "pprof", "-raw", path).CombinedOutput()
	if err != nil {
		t.Fatalf("go tool pprof -raw 解码失败: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Fatalf("go tool pprof -raw 输出为空")
	}
}

// TestStartCPUProfileInactive：命令不匹配 / 环境变量为空 / 路径不可创建
// 时不启动采样、不产生文件，也不 panic（诊断失败不阻塞构建）。
func TestStartCPUProfileInactive(t *testing.T) {
	dir := t.TempDir()

	t.Setenv("CODEINTEL_CPU_PROFILE", "")
	if stop, active := startCPUProfile("init"); active {
		stop()
		t.Error("空 CODEINTEL_CPU_PROFILE 不应启动采样")
	}
	if stop, active := startCPUProfile("query"); active {
		stop()
		t.Error("非构建命令不应启动采样")
	}

	// 路径指向目录 → os.Create 失败 → no-op
	t.Setenv("CODEINTEL_CPU_PROFILE", dir)
	stop, active := startCPUProfile("reindex")
	defer stop()
	if active {
		t.Error("无法创建的 profile 路径不应 active")
	}
}
