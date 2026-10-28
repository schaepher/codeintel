package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/progress"
	"go.uber.org/zap"
)

// cmdPrecompute 全量 relations 预计算（Q228）：
//
//	codeintel precompute relations --repo <path>
//
// 前台同步执行：逐表计算全库表间关联，每批更新 relation_progress
// 进度（done_count/total_count，按 build_id 持久化），完成写
// relation_candidates 缓存 + status=done。之后 CLI query relations
// --all / serve /api/er 全量查询直接命中缓存（0.1s 级），不再现场
// 计算。serve 端对未计算仓库的首次全量请求也会自动后台计算兜底。
func cmdPrecompute(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdPrecompute")
	defer logger.Debug("exit cmdPrecompute")
	// Q237：--repo 缺省当前目录（parseRepoFlag 默认 "."）
	repoDir, rest, err := parseRepoFlag(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	// P3-10：进度渲染统一走 domain.Progress（Q253）——只写 stderr，
	// stdout 只承载结果；--progress auto|plain|none 与 init/update 同口径。
	// 参数解析：位置参数才是子命令，--progress 的取值不能当子命令。
	sub := "relations"
	mode := progress.ModeAuto
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--progress" && i+1 < len(rest):
			mode = rest[i+1]
			i++
		case strings.HasPrefix(a, "--progress="):
			mode = strings.TrimPrefix(a, "--progress=")
		case a == "--json":
		default:
			sub = a
		}
	}
	if sub != "relations" {
		fmt.Fprintf(os.Stderr, "unknown precompute target: %s（当前支持 relations）\n", sub)
		return 2
	}
	db, err := sqlite.Open(repoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	rep := progress.New(os.Stderr, progress.Config{Mode: mode, Prefix: "[relations] 步骤", Title: "计算全量 relations"})
	// R100：编排逻辑迁 action（PrecomputeRelations）——cli 只提供进度
	// 实现（UI 行为）与结果渲染
	acts := action.New(sqlite.NewRepo(db))

	fmt.Printf("开始计算全量 relations……\n")
	res, err := acts.PrecomputeRelations(rep)
	rep.Finish()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switch res.Status {
	case "already-done":
		// 已完成直接提示
		fmt.Printf("全量 relations 已计算完成（%d 表）——查询直接命中缓存\n", res.Total)
	case "running":
		// 抢占失败（已有任务在跑）——提示等待
		fmt.Printf("计算任务已在运行中（进度 %d/%d）——可稍后重试查询或本命令查看进度\n", res.Done, res.Total)
	default:
		// 结果摘要
		fmt.Printf("完成：%d 表 · %d 条关联（fk %d / 键 %d / 写 %d / 读 %d）\n",
			res.Total, len(res.Rels), countByType(res.Rels, string(domain.RelationFK)), countByType(res.Rels, string(domain.RelationQuery)),
			countByType(res.Rels, string(domain.RelationWrite)), countByType(res.Rels, string(domain.RelationRead)))
	}
	return 0
}

// countByType 统计指定类型的关联数。
func countByType(rels []*domain.TableRelation, typ string) int {
	n := 0
	for _, r := range rels {
		if string(r.Type) == typ {
			n++
		}
	}
	return n
}
