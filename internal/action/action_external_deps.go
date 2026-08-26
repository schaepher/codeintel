package action

// R94 迁移：`query external-deps` 查询逻辑（原 cli/query_external_deps.go）
// ——外部依赖分析（redis 键 / kafka topic）：redis_key/kafka_topic 节点
// + 调用边聚合（谁访问哪个键/topic，读/写或生产/消费方向）。键/topic
// 是外部存储的"表"——外部依赖清单 + value-trace 可追。cli 只做参数
// 转发与输出（cmdExternalDeps）。

import (
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// RedisKeyEntry 一个 redis 键（调用方聚合）。
type RedisKeyEntry struct {
	Key     string   `json:"key"`
	Write   bool     `json:"write"`
	Callers []string `json:"callers"` // 调用方短名（函数/方法）
	Cmds    []string `json:"cmds"`    // 命令（去重）
}

// KafkaTopicEntry 一个 kafka topic（producer/consumer 聚合）。
type KafkaTopicEntry struct {
	Topic     string   `json:"topic"`
	Producers []string `json:"producers,omitempty"` // 生产者调用方短名
	Consumers []string `json:"consumers,omitempty"` // 消费者调用方短名
}

// ExternalDepsResult 外部依赖清单。
type ExternalDepsResult struct {
	Redis []RedisKeyEntry   `json:"redis"`
	Kafka []KafkaTopicEntry `json:"kafka"`
}

// ExternalDeps 读 redis_key/kafka_topic 节点 + 调用边 → 聚合。
func (a *Actions) ExternalDeps() (*ExternalDepsResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ExternalDeps")
	defer logger.Info("exit (Actions).ExternalDeps")
	res := &ExternalDepsResult{Redis: []RedisKeyEntry{}, Kafka: []KafkaTopicEntry{}}
	// redis 键（写标志）
	keyWrites := map[string]bool{}
	keyNodes, err := a.repo.GetRedisKeyNodes()
	if err != nil {
		return nil, err
	}
	for _, n := range keyNodes {
		keyWrites[n.Name] = n.Property("write") == "true"
	}
	// redis_call 边（调用方 → 键）
	keyCallers := map[string][]string{}
	keyCmds := map[string]map[string]bool{}
	for k := range keyWrites {
		keyCmds[k] = map[string]bool{}
	}
	redisFacts, err := a.repo.GetFactsByKinds(string(domain.FactRedisCall))
	if err != nil {
		return nil, err
	}
	for _, f := range redisFacts {
		key := metaStr(f.Metadata, "key")
		keyCallers[key] = append(keyCallers[key], shortNameOf(string(f.SourceID)))
		if cmd := metaStr(f.Metadata, "cmd"); cmd != "" {
			if keyCmds[key] == nil {
				keyCmds[key] = map[string]bool{}
			}
			keyCmds[key][cmd] = true
		}
	}
	ks := make([]string, 0, len(keyWrites))
	for k := range keyWrites {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		cmds := make([]string, 0, len(keyCmds[k]))
		for c := range keyCmds[k] {
			cmds = append(cmds, c)
		}
		sort.Strings(cmds)
		res.Redis = append(res.Redis, RedisKeyEntry{Key: k, Write: keyWrites[k], Callers: keyCallers[k], Cmds: cmds})
	}

	// kafka topic + 边
	topics := map[string]bool{}
	topicNodes, err := a.repo.GetKafkaTopicNodes()
	if err != nil {
		return nil, err
	}
	for _, n := range topicNodes {
		topics[n.Name] = true
	}
	topicProducers := map[string][]string{}
	topicConsumers := map[string][]string{}
	kafkaFacts, err := a.repo.GetFactsByKinds(string(domain.FactKafkaProduce), string(domain.FactKafkaConsume))
	if err != nil {
		return nil, err
	}
	for _, f := range kafkaFacts {
		topic := metaStr(f.Metadata, "topic")
		if f.Kind == domain.FactKafkaProduce {
			topicProducers[topic] = append(topicProducers[topic], shortNameOf(string(f.SourceID)))
		} else {
			topicConsumers[topic] = append(topicConsumers[topic], shortNameOf(string(f.SourceID)))
		}
	}
	ts := make([]string, 0, len(topics))
	for t := range topics {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	for _, t := range ts {
		sort.Strings(topicProducers[t])
		sort.Strings(topicConsumers[t])
		res.Kafka = append(res.Kafka, KafkaTopicEntry{Topic: t, Producers: topicProducers[t], Consumers: topicConsumers[t]})
	}
	return res, nil
}
