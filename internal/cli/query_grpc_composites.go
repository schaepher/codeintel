package cli

// R49 `codeintel query grpc-composites`——完整包含 grpc server 接口的
// 接口（重要信息：组合/扩展多个 grpc 服务的聚合接口）。数据源：构建期
// ast 检测（接口方法名集 ⊇ .pb.go XxxServer 接口方法名集）→ 接口节点
// 属性 pb_servers。R100：查询逻辑迁 action（Actions.GrpcComposites——
// 裸 SQL 收口仓储）；cli 只留渲染。JSON 契约：{composites: [{iface,
// servers, loc}]}。

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdGrpcComposites 实现 `codeintel query grpc-composites [--repo <path>] [--json]`。
func cmdGrpcComposites(acts *action.Actions, f queryFlags) int {
	res, err := acts.GrpcComposites()
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
