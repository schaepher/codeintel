package cli

// #0 `codeintel ask "<问题>"`——项目上下文问答：问题中的符号/表名
// 自动识别（精确匹配，--symbol/--table 显式指定优先）→ 附加核心查询
// 结果进 prompt → AI 回答透传 stdout。--json 输出
// {agent, prompt, response, duration_ms}。

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/logging"
)

// askPreamble 系统提示（透传给 AI 的第一段）。
const askPreamble = "你是 codeintel（Go 代码库智能索引）的问答助手。回答用户问题，尽量引用项目事实（file:line 或符号名）。"

// askDefaultTimeout 单次调用超时（--timeout 可调）。
const askDefaultTimeout = 60 * time.Second

// cmdAsk 实现 `codeintel ask "<问题>" [--agent codex|claude] [--symbol X]
// [--table Y] [--repo <path>] [--timeout <dur>] [--json]`。
func cmdAsk(args []string) int {
	repoPath := "."
	agentFlag := ""
	var syms, tbls []string
	timeout := askDefaultTimeout
	jsonOut := false
	var question []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			repoPath = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			repoPath = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--agent" && i+1 < len(args):
			agentFlag = args[i+1]
			i++
		case strings.HasPrefix(a, "--agent="):
			agentFlag = strings.TrimPrefix(a, "--agent=")
		case a == "--symbol" && i+1 < len(args):
			syms = append(syms, args[i+1])
			i++
		case strings.HasPrefix(a, "--symbol="):
			syms = append(syms, strings.TrimPrefix(a, "--symbol="))
		case a == "--table" && i+1 < len(args):
			tbls = append(tbls, args[i+1])
			i++
		case strings.HasPrefix(a, "--table="):
			tbls = append(tbls, strings.TrimPrefix(a, "--table="))
		case a == "--timeout" && i+1 < len(args):
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: 非法 --timeout %q（如 90s）\n", args[i+1])
				return 2
			}
			timeout = d
			i++
		case a == "--json":
			jsonOut = true
		case a == "--help" || a == "-h":
			fmt.Println("用法: codeintel ask \"<问题>\" [--agent codex|claude|auto] [--symbol X] [--table Y] [--repo <path>] [--timeout 60s] [--json]\n  项目上下文问答：自动识别问题中的符号/表名并附加查询结果")
			return 0
		default:
			question = append(question, a)
		}
	}
	agent, err := resolveAgent(agentFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	abs, _, err := resolveRepo(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if repoPath == "." {
			printRepoHint()
		}
		return 1
	}
	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	repo := sqlite.NewRepo(db)
	acts := action.New(repo)

	if len(question) == 0 {
		return askREPL(acts, repo, agent, timeout, abs) // 无问题 → 交互模式
	}
	q := strings.Join(question, " ")
	prompt := buildAskPrompt(acts, syms, tbls, q)
	start := time.Now()
	resp, err := agentRunner(agent, prompt, timeout, abs)
	dur := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	// W2：回答成功 → 收集进 qa_history（wiki --with-qa 参考资料）
	saveQA(repo, q, resp, askContextNames(acts, q), agent)
	if jsonOut {
		encodeJSON(map[string]any{
			"agent":       agent,
			"prompt":      prompt,
			"response":    resp,
			"duration_ms": dur.Milliseconds(),
		})
		return 0
	}
	fmt.Println(resp)
	return 0
}


// askContextNames 问题中命中项目事实的符号/表名（qa_history context

// buildAskPrompt 组装 prompt：系统提示 + 项目事实上下文 + 问题。
func buildAskPrompt(acts *action.Actions, syms, tbls []string, question string) string {
	ctxText := packAskContext(acts, syms, tbls, question)
	var b strings.Builder
	b.WriteString(askPreamble + "\n")
	if ctxText != "" {
		b.WriteString("\n以下是项目事实上下文（自动打包，可引用）：\n" + ctxText)
	}
	b.WriteString("\n用户问题: " + question + "\n")
	return b.String()
}

// askREPL 交互模式：逐行读 stdin，多轮追问复用同一会话（resume 机制
// 自动带 --resume——AI 记住前文，追问无需重复上下文）。每轮回答

// askTokenRe 问题中候选符号/表名 token：canonical ID、方法名 (T).m、
// 普通标识符/点路径。
var askTokenRe = regexp.MustCompile(`symbol:[A-Za-z0-9_./():\-]+|\([A-Za-z0-9_.]+\)\.[A-Za-z0-9_]+|[A-Za-z_][A-Za-z0-9_.]*`)

// askStopWords 常见词不当作符号候选。
var askStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"into": true, "this": true, "that": true, "what": true, "which": true,
	"does": true, "do": true, "are": true, "is": true, "how": true, "why": true,
	"who": true, "where": true, "when": true, "not": true, "can": true, "you": true,
}

// packAskContext 打包项目上下文：显式 --symbol/--table + 问题中自动
// 识别（精确匹配）——返回上下文文本（无匹配返回空串）。
func packAskContext(acts *action.Actions, syms, tbls []string, question string) string {
	var parts []string
	for _, s := range syms {
		if p := symbolContext(acts, s); p != "" {
			parts = append(parts, p)
		}
	}
	knownTables := knownTablesOf(acts)
	for _, t := range tbls {
		if p := tableContext(acts, t, knownTables); p != "" {
			parts = append(parts, p)
		}
	}
	// 自动识别：token 精确匹配表名/符号（避免误报——只收确定匹配）
	for _, tok := range askTokenRe.FindAllString(question, -1) {
		if len(tok) < 2 || askStopWords[tok] {
			continue
		}
		if _, ok := knownTables[tok]; ok {
			if p := tableContext(acts, tok, knownTables); p != "" {
				parts = append(parts, p)
			}
			continue
		}
		if p := symbolContext(acts, tok); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n") + "\n"
}

// knownTablesOf 已知表名集合（GetAllTableColumns 表.列 → 表名）。
func knownTablesOf(acts *action.Actions) map[string]bool {
	cols, err := acts.GetAllTableColumns()
	if err != nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, c := range cols {
		if i := strings.Index(c.Name, "."); i > 0 {
			out[c.Name[:i]] = true
		}
	}
	return out
}

// tableContext 一张表的上下文：列清单。
func tableContext(acts *action.Actions, table string, known map[string]bool) string {
	if !known[table] {
		return ""
	}
	cols, err := acts.GetAllTableColumns()
	if err != nil {
		return ""
	}
	var names []string
	for _, c := range cols {
		if strings.HasPrefix(c.Name, table+".") {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return fmt.Sprintf("=== 表 %s ===\n列: %s\n", table, strings.Join(names, ", "))
}

// symbolContext 一个符号的上下文：基本信息 + 调用者/被调用者（前 5）。
func symbolContext(acts *action.Actions, name string) string {
	d, err := acts.SymbolDetail(name)
	if err != nil {
		return ""
	}
	n := d.Node
	var callers, callees []string
	for i, f := range d.Callers {
		if i >= 5 {
			break
		}
		callers = append(callers, shortID(f.SourceID))
	}
	for i, f := range d.Callees {
		if i >= 5 {
			break
		}
		callees = append(callees, shortID(f.TargetID))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "=== 符号 %s ===\nID: %s\n", n.Name, n.ID)
	if n.FilePath != "" {
		fmt.Fprintf(&b, "文件: %s:%d\n", n.FilePath, n.LineStart)
	}
	if len(callers) > 0 {
		fmt.Fprintf(&b, "调用者: %s\n", strings.Join(callers, ", "))
	} else {
		b.WriteString("调用者: (无)\n")
	}
	if len(callees) > 0 {
		fmt.Fprintf(&b, "被调用者: %s\n", strings.Join(callees, ", "))
	}
	return strings.TrimSuffix(b.String(), "\n")
}
