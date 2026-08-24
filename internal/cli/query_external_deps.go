package cli

// R36 `codeintel query external-deps`——外部依赖分析（redis 键 / kafka
// topic）：redis_key/kafka_topic 节点 + 调用边聚合（谁访问哪个键/
// topic，读/写或生产/消费方向）。键/topic 是外部存储的"表"——外部
// 依赖清单 + value-trace 可追。

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// redisKeyEntry 一个 redis 键（调用方聚合）。
type redisKeyEntry struct {
	Key     string   `json:"key"`
	Write   bool     `json:"write"`
	Callers []string `json:"callers"` // 调用方（函数名:行）
	Cmds    []string `json:"cmds"`    // 命令（去重）
}

// kafkaTopicEntry 一个 kafka topic（producer/consumer 聚合）。
type kafkaTopicEntry struct {
	Topic    string   `json:"topic"`
	Producers []string `json:"producers,omitempty"` // 生产者调用方
	Consumers []string `json:"consumers,omitempty"` // 消费者调用方
}

// externalDepsResult 外部依赖清单。
type externalDepsResult struct {
	Redis  []redisKeyEntry  `json:"redis"`
	Kafka  []kafkaTopicEntry `json:"kafka"`
}

// externalDeps 读 redis_key/kafka_topic 节点 + 调用边 → 聚合。
func externalDeps(repo *sqlite.Repo) (*externalDepsResult, error) {
	res := &externalDepsResult{Redis: []redisKeyEntry{}, Kafka: []kafkaTopicEntry{}}
	// redis 键（写标志）
	keys := map[string]bool{}
	rows, err := repo.Query(`SELECT name, json_extract(properties, '$.write') FROM nodes WHERE kind = 'redis_key'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, write string
		if err := rows.Scan(&name, &write); err != nil {
			continue
		}
		keys[name] = write == "true"
	}
	rows.Close()
	keyCallers := map[string][]string{}
	keyCmds := map[string]map[string]bool{}
	for k := range keys {
		keyCmds[k] = map[string]bool{}
	}
	// redis_call 边（调用方 → 键）
	erows, err := repo.Query(`SELECT e.source_id, json_extract(e.metadata, '$.key'), json_extract(e.metadata, '$.cmd')
		FROM edges e WHERE e.kind = 'redis_call'`)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var src, key, cmd string
		if err := erows.Scan(&src, &key, &cmd); err != nil {
			continue
		}
		keyCallers[key] = append(keyCallers[key], shortSymbolNameID(src))
		if cmd != "" {
			keyCmds[key][cmd] = true
		}
	}
	erows.Close()
	ks := make([]string, 0, len(keys))
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		cmds := make([]string, 0, len(keyCmds[k]))
		for c := range keyCmds[k] {
			cmds = append(cmds, c)
		}
		sort.Strings(cmds)
		res.Redis = append(res.Redis, redisKeyEntry{Key: k, Write: keys[k], Callers: keyCallers[k], Cmds: cmds})
	}

	// kafka topic + 边
	topics := map[string]bool{}
	trows, err := repo.Query(`SELECT name FROM nodes WHERE kind = 'kafka_topic'`)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var name string
		if err := trows.Scan(&name); err != nil {
			continue
		}
		topics[name] = true
	}
	trows.Close()
	topicProducers := map[string][]string{}
	topicConsumers := map[string][]string{}
	krows, err := repo.Query(`SELECT e.source_id, json_extract(e.metadata, '$.topic'), e.kind
		FROM edges e WHERE e.kind IN ('kafka_produce','kafka_consume')`)
	if err != nil {
		return nil, err
	}
	for krows.Next() {
		var src, topic, kind string
		if err := krows.Scan(&src, &topic, &kind); err != nil {
			continue
		}
		if kind == "kafka_produce" {
			topicProducers[topic] = append(topicProducers[topic], shortSymbolNameID(src))
		} else {
			topicConsumers[topic] = append(topicConsumers[topic], shortSymbolNameID(src))
		}
	}
	krows.Close()
	ts := make([]string, 0, len(topics))
	for t := range topics {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	for _, t := range ts {
		sort.Strings(topicProducers[t])
		sort.Strings(topicConsumers[t])
		res.Kafka = append(res.Kafka, kafkaTopicEntry{Topic: t, Producers: topicProducers[t], Consumers: topicConsumers[t]})
	}
	return res, nil
}

// cmdExternalDeps 实现 `codeintel query external-deps [--repo <path>] [--json]`
func cmdExternalDeps(repoAbs string, f queryFlags) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	res, err := externalDeps(sqlite.NewRepo(db))
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
