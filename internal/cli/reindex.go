package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/progress"
)

// cmdReindex 实现 `codeintel reindex --repo <path>`：一步重建索引——
// 与 init 相同走全量构建（FullBuild 清空图数据表 DROP+CREATE 重建，
// 保留 build_metadata 与配置表），语义明确为"重建"。
func cmdReindex(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("reindex", flag.ExitOnError)
	// Q237：--repo 缺省当前工作目录
	repoPath := fs.String("repo", ".", "仓库根目录（含 go.mod；默认当前目录）")
	fs.Int("workers", defaultBuildWorkers(), "SSA 分析并发数（Q221/Q252e：默认 min(NumCPU, 8)；透传给 init）")
	fs.String("progress", progress.ModeAuto, "构建进度显示（Q253：auto|plain|none；透传给 init）")
	fs.Parse(args)
	*repoPath = ResolveRepoRef(*repoPath) // Q238：注册表短名/后缀/module

	abs, err := filepath.Abs(*repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: resolve repo path: %v\n", err)
		return 1
	}
	fmt.Printf("重建索引: %s\n", abs)
	// Q254c：reindex 的语义是"重建"——检测到 schema 不兼容（如 edges 改整数
	// 代理键）时删除旧库文件再走 init（图数据是派生数据，重建即迁移）。
	// 配置文件关系表 relation_rules 会一并丢失 → 打印醒目提示。
	if db, err := sqlite.Open(abs); err != nil {
		if strings.Contains(err.Error(), "schema mismatch") {
			fmt.Fprintf(os.Stderr,
				"warning: 旧索引 schema 不兼容——删除旧库后重建（配置表 relation_rules 会丢失）\n  原因: %v\n", err)
			for _, suf := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(filepath.Join(abs, ".codeintel", "codeintel.db"+suf))
			}
		}
	} else {
		db.Close()
	}
	return cmdInit(ctx, args)
}
