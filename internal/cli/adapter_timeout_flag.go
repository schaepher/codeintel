package cli

// todo23 §10.2：init/update 的显式 `--adapter-timeout 0|-1s` 必须报错退出
// （未显式传时 action.Build 用默认 10m）。

import (
	"flag"
	"fmt"
	"time"
)

// explicitAdapterTimeout 解析显式指定的 --adapter-timeout（未指定返回 0,false）。
func explicitAdapterTimeout(fs *flag.FlagSet, d time.Duration) (time.Duration, bool) {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "adapter-timeout" {
			set = true
		}
	})
	return d, set
}

// validateAdapterTimeout 显式指定时要求为正时长。
func validateAdapterTimeout(fs *flag.FlagSet, d time.Duration) error {
	v, set := explicitAdapterTimeout(fs, d)
	if set && v <= 0 {
		return fmt.Errorf("--adapter-timeout 必须为正时长（收到 %s）", v)
	}
	return nil
}
