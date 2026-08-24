package cli

// R36 query external-deps 测试：redis_key/kafka_topic 节点 + 调用边 →
// 聚合 JSON（读/写、producer/consumer）。测试先行。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedExternalDepsRepo 构造 redis_key/kafka_topic 节点 + 调用边。
func seedExternalDepsRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:redis:order:list", Kind: domain.KindRedisKey, Name: "order:list",
			Properties: map[string]any{"write": "false", "cmd": "GET"}},
		{ID: "symbol:redis:user:profile", Kind: domain.KindRedisKey, Name: "user:profile",
			Properties: map[string]any{"write": "true", "cmd": "SET"}},
		{ID: "symbol:kafka:order.created", Kind: domain.KindKafkaTopic, Name: "order.created"},
		// 调用方函数（边 FK 端点必须存在——否则静默跳过）
		{ID: "symbol:go:example.com/m:readCache", Kind: domain.KindFunction, Name: "readCache"},
		{ID: "symbol:go:example.com/m:writeCache", Kind: domain.KindFunction, Name: "writeCache"},
		{ID: "symbol:go:example.com/m:sendOrder", Kind: domain.KindFunction, Name: "sendOrder"},
		{ID: "symbol:go:example.com/m:consumeOrder", Kind: domain.KindFunction, Name: "consumeOrder"},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:readCache", TargetID: "symbol:redis:order:list",
			Kind: domain.FactRedisCall, Confidence: 1.0, Metadata: map[string]any{"key": "order:list", "cmd": "GET", "write": false}},
		{SourceID: "symbol:go:example.com/m:writeCache", TargetID: "symbol:redis:user:profile",
			Kind: domain.FactRedisCall, Confidence: 1.0, Metadata: map[string]any{"key": "user:profile", "cmd": "SET", "write": true}},
		{SourceID: "symbol:go:example.com/m:sendOrder", TargetID: "symbol:kafka:order.created",
			Kind: domain.FactKafkaProduce, Confidence: 1.0, Metadata: map[string]any{"topic": "order.created"}},
		{SourceID: "symbol:go:example.com/m:consumeOrder", TargetID: "symbol:kafka:order.created",
			Kind: domain.FactKafkaConsume, Confidence: 1.0, Metadata: map[string]any{"topic": "order.created"}},
	}
	if _, err := r.SaveBatchStats(nil, facts, nil); err != nil {
		t.Fatalf("save facts: %v", err)
	}
	return dir
}

// TestQueryExternalDeps：JSON 契约——redis（key/write/callers/cmds）+
// kafka（topic/producers/consumers）。
func TestQueryExternalDeps(t *testing.T) {
	dir := seedExternalDepsRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := externalDeps(sqlite.NewRepo(db))
	if err != nil {
		t.Fatalf("externalDeps: %v", err)
	}
	if len(res.Redis) != 2 {
		t.Fatalf("redis 键数 = %d; want 2:\n%+v", len(res.Redis), res.Redis)
	}
	if res.Redis[0].Key != "order:list" || res.Redis[0].Write {
		t.Errorf("order:list 应为读: %+v", res.Redis[0])
	}
	if len(res.Redis[1].Callers) != 1 || res.Redis[1].Callers[0] != "writeCache" {
		t.Errorf("user:profile 调用方 = %+v", res.Redis[1].Callers)
	}
	if len(res.Kafka) != 1 || res.Kafka[0].Topic != "order.created" {
		t.Fatalf("kafka topic = %+v", res.Kafka)
	}
	if len(res.Kafka[0].Producers) != 1 || res.Kafka[0].Producers[0] != "sendOrder" {
		t.Errorf("producers = %+v", res.Kafka[0].Producers)
	}
	if len(res.Kafka[0].Consumers) != 1 || res.Kafka[0].Consumers[0] != "consumeOrder" {
		t.Errorf("consumers = %+v", res.Kafka[0].Consumers)
	}
	// JSON 契约
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"redis"`, `"kafka"`, `"key"`, `"topic"`, `"producers"`, `"consumers"`, `"write"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON 缺 %q:\n%s", want, b)
		}
	}
}
