package action

// R100 待办13：迁移收尾——query cli-routes / grpc-composites 从 cli
// 直连 sqlite 迁 action（裸 SQL 收口到仓储层窄方法）；cli 只留参数
// 解析与输出渲染。

import (
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"
)

// CliCommandEntry 一条命令（子命令嵌套）。
type CliCommandEntry struct {
	Name        string            `json:"name"`
	Usage       string            `json:"usage,omitempty"`
	Action      string            `json:"action,omitempty"`
	Register    string            `json:"register,omitempty"`
	Subcommands []CliCommandEntry `json:"subcommands,omitempty"`
}

// CliRoutesResult 命令树（父命令组织——root 为无父命令项）。
type CliRoutesResult struct {
	Commands []CliCommandEntry `json:"commands"`
}

// CliRoutes 读 cli_command 节点 → 命令树（R35：urfave/cli/v2 命令树）。
func (a *Actions) CliRoutes() (*CliRoutesResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).CliRoutes")
	defer logger.Info("exit (Actions).CliRoutes")
	res := &CliRoutesResult{Commands: []CliCommandEntry{}}
	nodes, err := a.repo.GetCLICommandNodes()
	if err != nil {
		return nil, err
	}
	byParent := map[string][]CliCommandEntry{}
	var roots []CliCommandEntry
	for _, n := range nodes {
		e := CliCommandEntry{Name: n.Property("cli_name"), Usage: n.Property("cli_usage"),
			Action: n.Property("cli_action"), Register: n.Property("register")}
		if e.Name == "" {
			continue
		}
		if parent := n.Property("cli_parent"); parent == "" {
			roots = append(roots, e)
		} else {
			byParent[parent] = append(byParent[parent], e)
		}
	}
	// 组装树（子命令挂父——parent 是完整名 parent.name）
	var attach func(items []CliCommandEntry) []CliCommandEntry
	attach = func(items []CliCommandEntry) []CliCommandEntry {
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

// GrpcComposite 一个完整包含 grpc server 接口的接口。
type GrpcComposite struct {
	Iface   string   `json:"iface"`   // 接口（pkg:Name）
	Servers []string `json:"servers"` // 完整包含的 server 接口（pkg:Name）
	Loc     string   `json:"loc"`     // 定义位置 file:line
}

// GrpcCompositesResult 查询结果契约。
type GrpcCompositesResult struct {
	Composites []GrpcComposite `json:"composites"`
}

// GrpcComposites 读带 pb_servers 属性的接口节点 → 契约结构（R49：
// 组合/扩展多个 grpc 服务的聚合接口）。
func (a *Actions) GrpcComposites() (*GrpcCompositesResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).GrpcComposites")
	defer logger.Info("exit (Actions).GrpcComposites")
	res := &GrpcCompositesResult{Composites: []GrpcComposite{}}
	nodes, err := a.repo.GetPbServerInterfaces()
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		servers := ""
		if v, ok := n.Properties["pb_servers"].(string); ok {
			servers = v
		}
		if servers == "" {
			continue
		}
		var sl []string
		for _, s := range strings.Split(servers, ",") {
			if s = strings.TrimSpace(s); s != "" {
				sl = append(sl, s)
			}
		}
		loc := n.FilePath
		if n.LineStart > 0 {
			loc = fmt.Sprintf("%s:%d", n.FilePath, n.LineStart)
		}
		res.Composites = append(res.Composites, GrpcComposite{
			Iface: strings.TrimPrefix(string(n.ID), "symbol:go:"), Servers: sl, Loc: loc,
		})
	}
	sort.Slice(res.Composites, func(i, j int) bool { return res.Composites[i].Iface < res.Composites[j].Iface })
	return res, nil
}
