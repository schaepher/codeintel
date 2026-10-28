//go:build !linux

package sqlite

// todo24 14.4-B：非 Linux 平台的事件驱动采样不可用（没有 inotify）。
// 生产采集保持原有口径：/proc/self/fd 不可用时父进程输出 unavailable，
// 并写明原因——不能用 0 代替缺失观测。

import "errors"

var errTempWatchUnsupported = errors.New("平台无 inotify：事件驱动采样不可用（Linux 专用）")

// startTempWatch 非 Linux 平台没有 inotify：返回 noop，原因记入采样器。
func startTempWatch(s *selfTempSampler) func() {
	s.noteWatch(errTempWatchUnsupported)
	return func() {}
}
