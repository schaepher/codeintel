package action

// R94 迁移：`query kafka-topics` 查询逻辑（原 cli/query_kafka_topics.go）
// ——kafka topic 按生产/消费归属分类（R46 用户要求）：
// 1. 内部生产，内部消费：项目内有 producer 且有 consumer
// 2. 内部生产，外部消费：项目内有 producer、无 consumer
// 3. 外部生产，内部消费：项目内无 producer、有 consumer
// 数据源：kafka_topic 节点 + kafka_produce/kafka_consume 边（R36 发射）。
// 输出 JSON 契约：{topics: [{topic, category, producers: [{func, loc}],
// consumers: [{func, loc}]}]}。cli 只做参数转发与输出（cmdKafkaTopics）；
// 中文分类标签映射留在 cli 展示层。

import (
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// KafkaTopicCategory topic 归属分类（英文枚举——JSON 契约稳定；
// 展示层转中文）。
type KafkaTopicCategory string

const (
	KafkaCatInternal           KafkaTopicCategory = "internal"            // 内部生产，内部消费
	KafkaCatProducedInternally KafkaTopicCategory = "produced-internally" // 内部生产，外部消费
	KafkaCatConsumedInternally KafkaTopicCategory = "consumed-internally" // 外部生产，内部消费
)

// KafkaCaller 一个 producer/consumer 调用点。
type KafkaCaller struct {
	Func string `json:"func"` // 函数短名
	Loc  string `json:"loc"`  // file:line
}

// KafkaTopicFlow 一个 topic 的生产/消费流（R46 分类）。
type KafkaTopicFlow struct {
	Topic     string             `json:"topic"`
	Category  KafkaTopicCategory `json:"category"`
	Producers []KafkaCaller      `json:"producers"`
	Consumers []KafkaCaller      `json:"consumers"`
}

// KafkaTopicsResult 查询结果契约。
type KafkaTopicsResult struct {
	Topics []KafkaTopicFlow `json:"topics"`
}

// KafkaTopics 读 kafka_topic 节点 + produce/consume 边 → 分类聚合。
func (a *Actions) KafkaTopics() (*KafkaTopicsResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).KafkaTopics")
	defer logger.Info("exit (Actions).KafkaTopics")
	res := &KafkaTopicsResult{Topics: []KafkaTopicFlow{}}
	topics := map[string]bool{}
	topicNodes, err := a.repo.GetKafkaTopicNodes()
	if err != nil {
		return nil, err
	}
	for _, n := range topicNodes {
		topics[n.Name] = true
	}
	type callerRef struct {
		src   string
		topic string
		kind  string
	}
	var edges []callerRef
	facts, err := a.repo.GetFactsByKinds(string(domain.FactKafkaProduce), string(domain.FactKafkaConsume))
	if err != nil {
		return nil, err
	}
	for _, f := range facts {
		edges = append(edges, callerRef{src: string(f.SourceID), topic: metaStr(f.Metadata, "topic"), kind: string(f.Kind)})
	}
	// 边里出现的 topic 也纳入（节点可能未发射——补录）
	for _, e := range edges {
		if e.topic != "" {
			topics[e.topic] = true
		}
	}
	producers := map[string][]KafkaCaller{}
	consumers := map[string][]KafkaCaller{}
	for _, e := range edges {
		loc := ""
		if n, err := a.repo.GetSymbol(domain.CanonicalID(e.src)); err == nil && n != nil {
			loc = n.FilePath
		}
		c := KafkaCaller{Func: shortNameOf(e.src), Loc: loc}
		if e.kind == string(domain.FactKafkaProduce) {
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
		cat := KafkaCatConsumedInternally
		switch {
		case len(producers[t]) > 0 && len(consumers[t]) > 0:
			cat = KafkaCatInternal
		case len(producers[t]) > 0:
			cat = KafkaCatProducedInternally
		}
		res.Topics = append(res.Topics, KafkaTopicFlow{
			Topic: t, Category: cat,
			Producers: producers[t], Consumers: consumers[t],
		})
	}
	return res, nil
}
