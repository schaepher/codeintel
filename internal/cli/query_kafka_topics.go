package cli

// R46 `codeintel query kafka-topics`——kafka topic 按生产/消费归属分类
// （用户要求）：
// 1. 内部生产，内部消费：项目内有 producer 且有 consumer
// 2. 内部生产，外部消费：项目内有 producer、无 consumer
// 3. 外部生产，内部消费：项目内无 producer、有 consumer
// 数据源：kafka_topic 节点 + kafka_produce/kafka_consume 边（R36 发射）。
// 输出 JSON 契约：{topics: [{topic, category, producers: [{func, loc}],
// consumers: [{func, loc}]}]}。

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// kafkaTopicCategory topic 归属分类（英文枚举——JSON 契约稳定；
// 展示层转中文）。
type kafkaTopicCategory string

const (
	kafkaCatInternal          kafkaTopicCategory = "internal"           // 内部生产，内部消费
	kafkaCatProducedInternally kafkaTopicCategory = "produced-internally" // 内部生产，外部消费
	kafkaCatConsumedInternally kafkaTopicCategory = "consumed-internally" // 外部生产，内部消费
)

// kafkaTopicCategoryCN 分类中文名（展示）。
func (c kafkaTopicCategory) cn() string {
	switch c {
	case kafkaCatInternal:
		return "内部生产，内部消费"
	case kafkaCatProducedInternally:
		return "内部生产，外部消费"
	case kafkaCatConsumedInternally:
		return "外部生产，内部消费"
	}
	return string(c)
}

// kafkaCaller 一个 producer/consumer 调用点。
type kafkaCaller struct {
	Func string `json:"func"` // 函数短名
	Loc  string `json:"loc"`  // file:line
}

// kafkaTopicFlow 一个 topic 的生产/消费流（R46 分类）。
type kafkaTopicFlow struct {
	Topic     string           `json:"topic"`
	Category  kafkaTopicCategory `json:"category"`
	Producers []kafkaCaller    `json:"producers"`
	Consumers []kafkaCaller    `json:"consumers"`
}

// kafkaTopicsResult 查询结果契约。
type kafkaTopicsResult struct {
	Topics []kafkaTopicFlow `json:"topics"`
}

// kafkaTopics 读 kafka_topic 节点 + produce/consume 边 → 分类聚合。
func kafkaTopics(repo *sqlite.Repo) (*kafkaTopicsResult, error) {
	res := &kafkaTopicsResult{Topics: []kafkaTopicFlow{}}
	topics := map[string]bool{}
	rows, err := repo.Query(`SELECT name FROM nodes WHERE kind = 'kafka_topic'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		topics[name] = true
	}
	rows.Close()
	type callerRef struct{ src, topic, kind string }
	var edges []callerRef
	rows, err = repo.Query(`SELECT e.source_id, COALESCE(json_extract(e.metadata, '$.topic'), ''), e.kind
		FROM edges e WHERE e.kind IN ('kafka_produce','kafka_consume')`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src, topic, kind string
		if err := rows.Scan(&src, &topic, &kind); err != nil {
			continue
		}
		edges = append(edges, callerRef{src, topic, kind})
	}
	rows.Close()
	// 边里出现的 topic 也纳入（节点可能未发射——补录）
	for _, e := range edges {
		if e.topic != "" {
			topics[e.topic] = true
		}
	}
	producers := map[string][]kafkaCaller{}
	consumers := map[string][]kafkaCaller{}
	locOf := func(srcID string) string {
		lrows, err := repo.Query(`SELECT file_path FROM nodes WHERE id = ?`, srcID)
		if err != nil {
			return ""
		}
		defer lrows.Close()
		if lrows.Next() {
			var f string
			if err := lrows.Scan(&f); err == nil {
				return f
			}
		}
		return ""
	}
	for _, e := range edges {
		c := kafkaCaller{Func: shortSymbolNameID(e.src), Loc: locOf(e.src)}
		if e.kind == "kafka_produce" {
			producers[e.topic] = append(producers[e.topic], c)
		} else {
			consumers[e.topic] = append(consumers[e.topic], c)
		}
	}
	ts := make([]string, 0, len(topics))
	for t := range topics {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	for _, t := range ts {
		sort.Slice(producers[t], func(i, j int) bool { return producers[t][i].Func < producers[t][j].Func })
		sort.Slice(consumers[t], func(i, j int) bool { return consumers[t][i].Func < consumers[t][j].Func })
		cat := kafkaCatConsumedInternally
		switch {
		case len(producers[t]) > 0 && len(consumers[t]) > 0:
			cat = kafkaCatInternal
		case len(producers[t]) > 0:
			cat = kafkaCatProducedInternally
		}
		res.Topics = append(res.Topics, kafkaTopicFlow{
			Topic: t, Category: cat,
			Producers: producers[t], Consumers: consumers[t],
		})
	}
	return res, nil
}

// cmdKafkaTopics 实现 `codeintel query kafka-topics [--repo <path>] [--json]`。
func cmdKafkaTopics(repoAbs string, f queryFlags) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	res, err := kafkaTopics(sqlite.NewRepo(db))
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
		if t.Category.cn() != cur {
			cur = t.Category.cn()
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
func joinKafkaCallers(cs []kafkaCaller) string {
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
