package cli

// R94：cmdKafkaTopics 参数转发 + 输出（查询逻辑迁 internal/action
// ——Actions.KafkaTopics）。R46 `codeintel query kafka-topics`——kafka
// topic 按生产/消费归属分类。中文分类标签映射与调用点摘要属于展示，
// 留在本层。

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// kafkaCategoryCN 分类中文名（展示）。
func kafkaCategoryCN(c action.KafkaTopicCategory) string {
	switch c {
	case action.KafkaCatInternal:
		return "内部生产，内部消费"
	case action.KafkaCatProducedInternally:
		return "内部生产，外部消费"
	case action.KafkaCatConsumedInternally:
		return "外部生产，内部消费"
	}
	return string(c)
}

// cmdKafkaTopics 实现 `codeintel query kafka-topics [--repo <path>] [--json]`。
func cmdKafkaTopics(acts *action.Actions, f queryFlags) int {
	res, err := acts.KafkaTopics()
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
	for _, t := range res.Topics {
		if cn := kafkaCategoryCN(t.Category); cn != cur {
			cur = cn
			fmt.Printf("\n[%s]\n", cur)
		}
		fmt.Printf("  %s\n", t.Topic)
		if len(t.Producers) > 0 {
			fmt.Printf("    生产: %s\n", joinKafkaCallers(t.Producers))
		}
		if len(t.Consumers) > 0 {
			fmt.Printf("    消费: %s\n", joinKafkaCallers(t.Consumers))
		}
	}
	fmt.Printf("\n共 %d 个 kafka topic\n", len(res.Topics))
	return 0
}

// joinKafkaCallers 调用点摘要（前 3 个）。
func joinKafkaCallers(cs []action.KafkaCaller) string {
	var parts []string
	for i, c := range cs {
		if i >= 3 {
			parts = append(parts, fmt.Sprintf("等 %d 处", len(cs)-3))
			break
		}
		loc := c.Loc
		if loc == "" {
			loc = "?"
		}
		parts = append(parts, c.Func+"("+loc+")")
	}
	return strings.Join(parts, "、")
}
