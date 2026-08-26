package cli

// R94：cmdExternalDeps 参数转发 + 输出（查询逻辑迁 internal/action
// ——Actions.ExternalDeps）。R36 `codeintel query external-deps`
// ——外部依赖分析（redis 键 / kafka topic）：redis_key/kafka_topic
// 节点 + 调用边聚合（谁访问哪个键/topic，读/写或生产/消费方向）。

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdExternalDeps 实现 `codeintel query external-deps [--repo <path>] [--json]`
func cmdExternalDeps(acts *action.Actions, f queryFlags) int {
	res, err := acts.ExternalDeps()
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
	fmt.Printf("redis 键（%d）：\n", len(res.Redis))
	for _, r := range res.Redis {
		w := "读"
		if r.Write {
			w = "写"
		}
		fmt.Printf("  %-32s [%s] %s（%s）\n", r.Key, w, strings.Join(r.Callers, ", "), strings.Join(r.Cmds, "/"))
	}
	fmt.Printf("kafka topic（%d）：\n", len(res.Kafka))
	for _, t := range res.Kafka {
		fmt.Printf("  %s  生产者: %s  消费者: %s\n", t.Topic,
			strings.Join(t.Producers, ", "), strings.Join(t.Consumers, ", "))
	}
	return 0
}
