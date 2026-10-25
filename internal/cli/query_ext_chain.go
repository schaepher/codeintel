package cli

// R83 `query ext-chain <符号>`——外部系统调用链：从符号出发查最终
// 调用的 grpc/http（缓存优先），对每个 grpc 服务找服务端**同名 RPC 方法**
// 实现，递归查该方法是否再调用其他 grpc/http——直到没有为止。
// http 是外部系统终点（无服务端可查）。
// todo10-4：目标仓库经全局注册表跨仓解析（openRepoActions 注入 action）；
// 未续接（方法缺失/无实现仓库/循环/深度）在输出里带 code 诊断。
// R95：递归链逻辑迁 action（Actions.ExtChain）；cli 只做参数转发与
// 树状输出（writeExtChain）。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// cmdExtChain 实现 `query ext-chain <符号> [--json]`。
// todo10-4：跨仓续接——候选仓库来自全局注册表（已索引仓库），
// 目标仓库按需打开（只读视图）。
func cmdExtChain(acts *action.Actions, repoAbs, symbol string, jsonOut bool) int {
	root, err := acts.ExtChain(action.ExtChainRequest{
		Symbol: symbol, RepoAbs: repoAbs,
		CandidateRepos: extChainCandidateRepos(repoAbs),
		Open:           openRepoActions,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if jsonOut {
		encodeJSON(root)
		return 0
	}
	if len(root.Grpc) == 0 && len(root.HTTP) == 0 {
		fmt.Printf("%s 未调用任何外部系统（grpc/http）\n", symbol)
		return 0
	}
	fmt.Printf("== %s 外部系统调用链 ==\n", symbol)
	writeExtChain(root, 0, repoAbs)
	return 0
}

// writeExtChain 缩进树渲染（层级：服务 → 服务端方法 → 再调用）。
// rootRepo 用于标注"跨仓续接"（目标仓库与起始仓库不同时显示路径）。
func writeExtChain(n *action.ExtChainNode, depth int, rootRepo string) {
	pad := strings.Repeat("  ", depth)
	for _, g := range n.Grpc {
		fmt.Printf("%s▸ grpc %s", pad, g.Service)
		if g.Method != "" {
			fmt.Printf(".%s", g.Method)
		}
		if g.Line > 0 {
			fmt.Printf("（%s:%d）", g.CalledBy, g.Line)
		} else if g.CalledBy != "" {
			fmt.Printf("（%s）", g.CalledBy)
		}
		fmt.Println()
		if txt := extChainStopText(g.Stop); txt != "" {
			fmt.Printf("%s%s\n", pad, txt)
			continue
		}
		for _, s := range g.Server {
			// todo10-4：输出目标方法与匹配依据（跨仓时标注仓库）
			fmt.Printf("%s  服务端 %s", pad, shortSymbolNameID(s.Symbol))
			if g.TargetMethod != "" {
				fmt.Printf(" [%s", shortSymbolNameID(g.TargetMethod))
				if g.MatchReason != "" {
					fmt.Printf(" · %s", g.MatchReason)
				}
				fmt.Print("]")
			}
			if g.TargetRepo != "" && s.Repo != rootRepo {
				fmt.Printf(" @%s", g.TargetRepo)
			}
			fmt.Println()
			writeExtChain(&s, depth+2, rootRepo)
		}
	}
	for _, h := range n.HTTP {
		loc := ""
		if h.Line > 0 {
			loc = fmt.Sprintf("（%s:%d）", h.CalledBy, h.Line)
		} else {
			loc = "（" + h.CalledBy + "）"
		}
		fmt.Printf("%s▸ http %s %s %s\n", pad, h.Service, h.Path, loc)
	}
}

// extChainCandidateRepos 其它已注册且已构建的仓库（跨仓续接候选；起始仓库
// 由 action 优先查，这里排除）。
func extChainCandidateRepos(self string) []string {
	r := openGlobalRegistry()
	if r == nil {
		return nil
	}
	defer r.Close()
	repos, err := r.ListRepos()
	if err != nil {
		return nil
	}
	var out []string
	for _, rp := range repos {
		if rp.Path == "" || rp.Path == self || rp.BuildID == "" {
			continue
		}
		out = append(out, rp.Path)
	}
	return out
}

// openRepoActions 打开目标仓库的查询视图（跨仓续接用；调用方负责关闭）。
func openRepoActions(repoPath string) (*action.Actions, func() error, error) {
	db, err := sqlite.Open(repoPath)
	if err != nil {
		return nil, nil, err
	}
	return action.New(sqlite.NewRepo(db)), db.Close, nil
}

// extChainStopText 未续接原因文案（todo10-4：诊断要能看出"为什么到此为止"）。
func extChainStopText(st *action.SeqStop) string {
	if st == nil || st.Code == "" {
		return ""
	}
	txt := "  ⚠ 未续接：" + st.Code
	if st.Detail != "" {
		txt += "（" + st.Detail + "）"
	}
	return txt
}
