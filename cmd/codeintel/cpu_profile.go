package main

// Q221 诊断：构建类命令的 CPU profile（CODEINTEL_CPU_PROFILE=<file>）。
//
// 旧实现把 Start/Stop 写在 main 里，停止时**再次 os.Create 同一个路径**
// ——第二次 Create 截断文件，而 StopCPUProfile 仍往启动时那个文件句柄写，
// 结果 profile 被截断/损坏（pprof 解析失败）。本文件把文件句柄作为
// 采样生命周期状态持有：启动时打开一次，停止时 flush 后关闭同一句柄。
//
// 任何启动失败（命令不匹配 / 环境变量为空 / 文件打不开 / 已有采样在跑）
// 都返回 no-op，不阻塞构建，也不影响其它诊断采样。

import (
	"os"
	"runtime/pprof"
)

// startCPUProfile 在构建类命令（init/reindex/update）+ CODEINTEL_CPU_PROFILE
// 设置时启动 CPU 采样。返回 stop（停止采样并关闭启动时打开的文件）与
// active（是否真的采到了——失败时 stop 为 no-op 且 active=false）。
func startCPUProfile(sub string) (stop func(), active bool) {
	if sub != "init" && sub != "reindex" && sub != "update" {
		return func() {}, false
	}
	path := os.Getenv("CODEINTEL_CPU_PROFILE")
	if path == "" {
		return func() {}, false
	}
	f, err := os.Create(path)
	if err != nil {
		return func() {}, false
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return func() {}, false
	}
	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}, true
}
