package cli

// R49 `codeintel query grpc-composites`——完整包含 grpc server 接口的
// 接口（重要信息：组合/扩展多个 grpc 服务的聚合接口）。数据源：构建期
// ast 检测（接口方法名集 ⊇ .pb.go XxxServer 接口方法名集）→ 接口节点
// 属性 pb_servers。输出 JSON 契约：{composites: [{iface, servers, loc}]}。

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// grpcComposite 一个完整包含 grpc server 接口的接口。
type grpcComposite struct {
	Iface   string   `json:"iface"`   // 接口（pkg:Name）
	Servers []string `json:"servers"` // 完整包含的 server 接口（pkg:Name）
	Loc     string   `json:"loc"`     // 定义位置 file:line
}

// grpcCompositesResult 查询结果契约。
type grpcCompositesResult struct {
	Composites []grpcComposite `json:"composites"`
}

// grpcComposites 读 interface 节点带 pb_servers 属性 → 契约结构。
func grpcComposites(repo *sqlite.Repo) (*grpcCompositesResult, error) {
	res := &grpcCompositesResult{Composites: []grpcComposite{}}
	rows, err := repo.Query(`SELECT id, COALESCE(json_extract(properties, '$.pb_servers'), ''),
		COALESCE(file_path, ''), COALESCE(line_start, 0) FROM nodes
		WHERE kind = 'interface' AND json_extract(properties, '$.pb_servers') IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, servers, file string
		var line int
		if err := rows.Scan(&id, &servers, &file, &line); err != nil || servers == "" {
			continue
		}
		var sl []string
		for _, s := range strings.Split(servers, ",") {
			if s = strings.TrimSpace(s); s != "" {
				sl = append(sl, s)
			}
		}
		loc := file
		if line > 0 {
			loc = fmt.Sprintf("%s:%d", file, line)
		}
		res.Composites = append(res.Composites, grpcComposite{
			Iface: strings.TrimPrefix(id, "symbol:go:"), Servers: sl, Loc: loc,
		})
	}
	sort.Slice(res.Composites, func(i, j int) bool { return res.Composites[i].Iface < res.Composites[j].Iface })
	return res, nil
}

// cmdGrpcComposites 实现 `codeintel query grpc-composites [--repo <path>] [--json]`。
func cmdGrpcComposites(repoAbs string, f queryFlags) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	res, err := grpcComposites(sqlite.NewRepo(db))
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
	for _, c := range res.Composites {
		fmt.Printf("%s（%s）\n", c.Iface, c.Loc)
		fmt.Printf("  完整包含: %s\n", strings.Join(c.Servers, "、"))
	}
	fmt.Printf("\n共 %d 个组合接口\n", len(res.Composites))
	return 0
}
