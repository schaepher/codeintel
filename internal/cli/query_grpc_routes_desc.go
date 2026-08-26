package cli

// R29 `codeintel query grpc-routes`——服务端 gRPC 路由清单（契约化
// JSON，Agent 直接解析）。R92：查询逻辑迁 action（Actions.GrpcRoutes）；
// cli 只做参数转发与输出格式化。

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdGrpcRoutes 实现 `codeintel query grpc-routes [--repo <path>] [--json]`
// ——服务端 gRPC 路由清单（契约化 JSON，Agent 直接解析）。
func cmdGrpcRoutes(acts *action.Actions, repoAbs string, f queryFlags) int {
	res, err := acts.GrpcRoutes(repoAbs)
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
	for _, s := range res.Services {
		fmt.Printf("[%s] 实现 %s（%s） 注册 %s\n", s.Name, s.Impl, s.ImplFile, s.Register)
		for _, m := range s.Methods {
			fmt.Printf("  %s（%s）\n", m.Name, m.Handler)
		}
	}
	fmt.Printf("\n共 %d 个 gRPC 服务\n", len(res.Services))
	return 0
}
