package ast

// R35 urfave/cli/v2 命令树识别测试：App 字面量 Commands + 包级
// Commands 变量 + 子命令（Subcommands）→ cli_command 节点。
// 测试先行（fixture 用 replace 本地 stub 模拟真实包路径）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestCLICommandTree：App 字面量 + 包级变量 + 子命令 → cli_command
// 节点（cli_name/cli_usage/cli_action/cli_parent）。
func TestCLICommandTree(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": `module example.com/mtest

go 1.21

require github.com/urfave/cli/v2 v2.25.0

replace github.com/urfave/cli/v2 => ./clistub
`,
		"clistub/go.mod": "module github.com/urfave/cli/v2\n\ngo 1.21\n",
		"clistub/cli.go": `package cli

type Context struct{}

type App struct {
	Name     string
	Commands []*Command
}

type Command struct {
	Name        string
	Usage       string
	Action      func(*Context) error
	Subcommands []*Command
}

func (a *App) Run(args []string) error { return nil }
`,
		"cmd/main.go": `package main

import "github.com/urfave/cli/v2"

func serve(c *cli.Context) error { return nil }
func query(c *cli.Context) error { return nil }
func list(c *cli.Context) error  { return nil }

// 包级 Commands 变量
var Commands = []*cli.Command{
	{Name: "serve", Usage: "启动服务", Action: serve},
}

func main() {
	app := &cli.App{
		Name: "myapp",
		Commands: []*cli.Command{
			{Name: "query", Usage: "查询数据", Action: query},
			{Name: "db", Usage: "数据库操作", Subcommands: []*cli.Command{
				{Name: "list", Usage: "列表", Action: list},
			}},
		},
	}
	app.Run(nil)
}
`,
	})

	byName := map[string]map[string]string{}
	for _, n := range nodes {
		if n.Kind != domain.KindCLICommand {
			continue
		}
		p := n.Properties
		byName[n.Name] = map[string]string{
			"usage":  asStr(p["cli_usage"]),
			"action": asStr(p["cli_action"]),
			"parent": asStr(p["cli_parent"]),
		}
	}
	// 包级 Commands：serve
	if c, ok := byName["serve"]; !ok || c["usage"] != "启动服务" || c["action"] != "serve" {
		t.Errorf("包级 Commands 未识别 serve: %v（全部: %v）", byName["serve"], byName)
	}
	// App 字面量：query + db.list（嵌套）
	if c, ok := byName["query"]; !ok || c["usage"] != "查询数据" || c["action"] != "query" {
		t.Errorf("App Commands 未识别 query: %v", byName["query"])
	}
	if c, ok := byName["db"]; !ok || c["parent"] != "" {
		t.Errorf("顶层 db 命令: %v", byName["db"])
	}
	if c, ok := byName["db.list"]; !ok || c["parent"] != "db" || c["action"] != "list" {
		t.Errorf("嵌套子命令 db.list 未识别: %v", byName["db.list"])
	}
}
