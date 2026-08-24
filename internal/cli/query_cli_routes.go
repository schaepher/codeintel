package cli

// R35 `codeintel query cli-routes`——urfave/cli/v2 命令树（待办 3：
// 不依赖文件路径——构建期识别 cli_command 节点，查询层直接读）。
// JSON 契约：命令名/用法/action/位置 + 子命令树。

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// cliCommandEntry 一条命令（子命令嵌套）。
type cliCommandEntry struct {
	Name        string            `json:"name"`
	Usage       string            `json:"usage,omitempty"`
	Action      string            `json:"action,omitempty"`
	Register    string            `json:"register,omitempty"`
	Subcommands []cliCommandEntry `json:"subcommands,omitempty"`
}

// cliRoutesResult 命令树（父命令组织——root 为无父命令项）。
type cliRoutesResult struct {
	Commands []cliCommandEntry `json:"commands"`
}

// cliRoutes 读 cli_command 节点 → 命令树。
func cliRoutes(repo *sqlite.Repo) (*cliRoutesResult, error) {
	res := &cliRoutesResult{Commands: []cliCommandEntry{}}
	rows, err := repo.Query(`SELECT name, json_extract(properties, '$.cli_name'), COALESCE(json_extract(properties, '$.cli_usage'), ''),
		COALESCE(json_extract(properties, '$.cli_action'), ''), COALESCE(json_extract(properties, '$.cli_parent'), ''), COALESCE(json_extract(properties, '$.register'), '')
		FROM nodes WHERE kind = 'cli_command'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byParent := map[string][]cliCommandEntry{}
	var roots []cliCommandEntry
	for rows.Next() {
		var id, name, usage, action, parent, register string
		if err := rows.Scan(&id, &name, &usage, &action, &parent, &register); err != nil {
			continue
		}
		e := cliCommandEntry{Name: name, Usage: usage, Action: action, Register: register}
		if parent == "" {
			roots = append(roots, e)
		} else {
			byParent[parent] = append(byParent[parent], e)
		}
	}
	// 组装树（子命令挂父——parent 是完整名 parent.name）
	var attach func(items []cliCommandEntry) []cliCommandEntry
	attach = func(items []cliCommandEntry) []cliCommandEntry {
		for i := range items {
			full := items[i].Name
			items[i].Subcommands = attach(byParent[full])
		}
		return items
	}
	res.Commands = attach(roots)
	sort.Slice(res.Commands, func(i, j int) bool { return res.Commands[i].Name < res.Commands[j].Name })
	return res, nil
}

// cmdCLIRoutes 实现 `codeintel query cli-routes [--repo <path>] [--json]`
func cmdCLIRoutes(repoAbs string, f queryFlags) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	res, err := cliRoutes(sqlite.NewRepo(db))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.json {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	var walk func(cmds []cliCommandEntry, depth int)
	walk = func(cmds []cliCommandEntry, depth int) {
		for _, c := range cmds {
			fmt.Printf("%s%s", strings.Repeat("  ", depth), c.Name)
			if c.Usage != "" {
				fmt.Printf("  %s", c.Usage)
			}
			if c.Action != "" {
				fmt.Printf("  → %s", c.Action)
			}
			fmt.Println()
			walk(c.Subcommands, depth+1)
		}
	}
	walk(res.Commands, 0)
	if len(res.Commands) == 0 {
		fmt.Println("未识别到 urfave/cli/v2 命令树")
	}
	return 0
}
