package sqlite

// todo24 14.4-B：**事件驱动**的临时文件采样（Linux / inotify）。
//
// 250µs 轮询在"查询短于间隔"时只能靠查询起止快照覆盖；更短的文件（创建、
// 扩容、关闭都发生在两次轮询之间）会被漏掉。这里用 inotify 监听 TMPDIR：
// 内核把 create/modify/close/delete 事件排队，监听到就立刻做一次快照，
// 因此采样时机不再取决于轮询间隔。
//
// 仅用于测量：不改生产查询、不 sleep。inotify 不可用（非 Linux、fd 上限、
// 权限）时返回 noop——轮询与边界快照仍在，原因由 temp_watch_note 记录。

import (
	"encoding/binary"
	"path/filepath"
	"sync"
	"syscall"
)

// startTempWatch 在 tmpDir 上建立 inotify 监听；每次事件后触发一次
// sampler.snapshot("event")。返回幂等的停止函数。
func startTempWatch(s *selfTempSampler) func() {
	noop := func() {}
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		s.noteWatch(err)
		return noop
	}
	const mask = syscall.IN_CREATE | syscall.IN_MODIFY | syscall.IN_ATTRIB |
		syscall.IN_CLOSE_WRITE | syscall.IN_DELETE | syscall.IN_MOVED_FROM |
		syscall.IN_MOVED_TO | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF
	wd, err := syscall.InotifyAddWatch(fd, s.tmpDir, mask)
	if err != nil {
		_ = syscall.Close(fd)
		s.noteWatch(err)
		return noop
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_, _ = syscall.InotifyRmWatch(fd, uint32(wd))
			_ = syscall.Close(fd)
		})
	}
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, err := syscall.Read(fd, buf)
			if err != nil {
				if err == syscall.EINTR {
					continue
				}
				return // fd 已关闭（stop）或不可读
			}
			if n <= 0 {
				continue
			}
			for _, name := range inotifyNames(s.tmpDir, buf[:n]) {
				s.addWatched(name)
			}
			s.snapshot("event")
		}
	}()
	return stop
}

// inotifyNames 解析 inotify 事件缓冲区里的文件名（相对被监听目录），返回
// 绝对路径；目录事件忽略（只关心 TMPDIR 顶层文件）。纯解析函数，便于单测。
func inotifyNames(dir string, buf []byte) []string {
	const hdr = 16 // wd(4) + mask(4) + cookie(4) + len(4)
	var out []string
	off := 0
	for off+hdr <= len(buf) {
		mask := binary.NativeEndian.Uint32(buf[off+4:])
		nameLen := int(binary.NativeEndian.Uint32(buf[off+12:]))
		nameStart := off + hdr
		off = nameStart + nameLen
		if nameLen <= 0 || nameStart+nameLen > len(buf) {
			continue
		}
		if mask&syscall.IN_ISDIR != 0 {
			continue
		}
		raw := buf[nameStart : nameStart+nameLen]
		// name 以 NUL 结尾并补齐到 4 字节边界。
		for len(raw) > 0 && raw[len(raw)-1] == 0 {
			raw = raw[:len(raw)-1]
		}
		if len(raw) == 0 {
			continue
		}
		out = append(out, filepath.Join(dir, string(raw)))
	}
	return out
}
