package ast

// R92：ast 适配器源码哈希——Q182 analyzer 版本判定扩展。增量构建只
// 重算变更文件所在包——ast 分析逻辑（grpc 识别/调用分析/接口具体化）
// 修改后，未变更包的数据不会以新逻辑重算 → 须全量重建。此前 marker
// 只覆盖 ssa 包（ast 修改后 update 不自动降级 → 手动 reindex）。
// 本哈希纳入 analyzerVersionHash——修改 ast 后 update 自动降级全量。

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
)

//go:embed *.go
var astSourceFS embed.FS

var astHashOnce sync.Once
var astHash string

// SourceHash ast 包生产源码内容 hash（编译时 embed 快照——与 cwd 无
// 关、覆盖未提交改动；_test.go 排除——测试不影响产物）。
func SourceHash() string {
	astHashOnce.Do(func() {
		entries, err := astSourceFS.ReadDir(".")
		if err != nil {
			astHash = "unknown"
			return
		}
		var files []string
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			files = append(files, e.Name())
		}
		sort.Strings(files)
		h := sha256.New()
		for _, f := range files {
			data, err := astSourceFS.ReadFile(f)
			if err != nil {
				astHash = "unknown"
				return
			}
			h.Write([]byte(f))
			h.Write(data)
			h.Write([]byte{0})
		}
		astHash = hex.EncodeToString(h.Sum(nil))[:16]
	})
	return astHash
}
