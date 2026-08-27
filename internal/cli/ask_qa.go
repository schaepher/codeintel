package cli

// ask 的 Q&A 收集与交互模式（从 ask.go 拆出——行数治理）。

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// 收集进 qa_history（W2）。repoAbs：agent 子进程 cwd（仓库内文件读取免权限）。
func askREPL(acts *action.Actions, repo *sqlite.Repo, agent string, timeout time.Duration, repoAbs string) int {
	fmt.Println("codeintel ask 交互模式——多轮追问复用同一会话（输入 exit/quit 退出，Ctrl-D 结束）")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("ask> ")
		if !scanner.Scan() {
			break // EOF（Ctrl-D）
		}
		q := strings.TrimSpace(scanner.Text())
		if q == "" {
			continue
		}
		if q == "exit" || q == "quit" || q == "q" {
			break
		}
		resp, err := agentRunner(agent, buildAskPrompt(acts, nil, nil, q), timeout, repoAbs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		saveQA(repo, q, resp, askContextNames(acts, q), agent)
		fmt.Println(resp)
	}
	return 0
}

// saveQA 写入历史问答（失败静默——收集不影响主流程）。
func saveQA(repo *sqlite.Repo, question, answer, context, agent string) {
	if repo == nil || question == "" || answer == "" {
		return
	}
	_ = repo.SaveQA(&domain.QARecord{
		Question:  question,
		Answer:    answer,
		Context:   context,
		Agent:     agent,
		CreatedAt: time.Now().Unix(),
	})
}

// 字段——参考资料按此相关性匹配）。
func askContextNames(acts *action.Actions, question string) string {
	known := knownTablesOf(acts)
	var names []string
	for _, tok := range askTokenRe.FindAllString(question, -1) {
		if len(tok) < 2 || askStopWords[tok] {
			continue
		}
		if known[tok] || symbolContext(acts, tok) != "" {
			names = append(names, tok)
		}
	}
	return strings.Join(names, ",")
}
