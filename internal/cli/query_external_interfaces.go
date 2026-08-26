package cli

// R94：cmdExternalInterfaces 参数转发 + 输出（查询逻辑迁
// internal/action ——Actions.ExternalInterfaces）。R45 `codeintel query
// external-interfaces`——外部系统接口调用识别。joinCallers /
// renderExternalInterfacesMD / renderExternalInterfacesHTML 在
// wiki_external.go（展示）。

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdExternalInterfaces 实现 `codeintel query external-interfaces
// [--repo <path>] [--json]`——外部系统接口调用清单。
func cmdExternalInterfaces(acts *action.Actions, f queryFlags) int {
	res, err := acts.ExternalInterfaces()
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
	cur := ""
	for _, ei := range res.Interfaces {
		key := ei.Kind + "|" + ei.Service
		if key != cur {
			cur = key
			fmt.Printf("\n[%s] %s\n", ei.Kind, ei.Service)
		}
		fmt.Printf("  %s", ei.Method)
		if ei.ReqType != "" {
			fmt.Printf("（请求 %s）", ei.ReqType)
		}
		fmt.Printf(" ← %s\n", joinCallers(ei.Callers))
	}
	fmt.Printf("\n共 %d 个外部接口调用\n", len(res.Interfaces))
	return 0
}
