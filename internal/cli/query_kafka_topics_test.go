package cli

// R46 query kafka-topics 测试：topic 三分类——内部产内消（有 producer
// 有 consumer）/内部产外消（只有 producer）/外部产内消（只有 consumer）。
// 测试先行。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedKafkaTopicsRepo kafka topic fixture：三类各一个 + 调用函数。
func seedKafkaTopicsRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/evt:topic.1", Kind: domain.KindKafkaTopic, Name: "order.created"},
		{ID: "symbol:go:example.com/m/evt:topic.2", Kind: domain.KindKafkaTopic, Name: "order.expired"},
		{ID: "symbol:go:example.com/m/evt:topic.3", Kind: domain.KindKafkaTopic, Name: "payment.result"},
		{ID: "symbol:go:example.com/m/app:createOrder", Kind: domain.KindFunction, Name: "createOrder", FilePath: "app/order.go", LineStart: 10},
		{ID: "symbol:go:example.com/m/app:consumeOrder", Kind: domain.KindFunction, Name: "consumeOrder", FilePath: "app/order.go", LineStart: 20},
		{ID: "symbol:go:example.com/m/app:expireOrder", Kind: domain.KindFunction, Name: "expireOrder", FilePath: "app/order.go", LineStart: 30},
		{ID: "symbol:go:example.com/m/app:consumePay", Kind: domain.KindFunction, Name: "consumePay", FilePath: "app/pay.go", LineStart: 40},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	facts := []*domain.Fact{
		// order.created：内部生产（createOrder）+ 内部消费（consumeOrder）
		{SourceID: "symbol:go:example.com/m/app:createOrder", TargetID: "symbol:go:example.com/m/evt:topic.1",
			Kind: domain.FactKafkaProduce, Confidence: 1.0, Metadata: map[string]any{"topic": "order.created"}},
		{SourceID: "symbol:go:example.com/m/app:consumeOrder", TargetID: "symbol:go:example.com/m/evt:topic.1",
			Kind: domain.FactKafkaConsume, Confidence: 1.0, Metadata: map[string]any{"topic": "order.created"}},
		// order.expired：只有内部生产（expireOrder）——外部消费
		{SourceID: "symbol:go:example.com/m/app:expireOrder", TargetID: "symbol:go:example.com/m/evt:topic.2",
			Kind: domain.FactKafkaProduce, Confidence: 1.0, Metadata: map[string]any{"topic": "order.expired"}},
		// payment.result：只有内部消费（consumePay）——外部生产
		{SourceID: "symbol:go:example.com/m/app:consumePay", TargetID: "symbol:go:example.com/m/evt:topic.3",
			Kind: domain.FactKafkaConsume, Confidence: 1.0, Metadata: map[string]any{"topic": "payment.result"}},
	}
	if _, err := r.SaveBatchStats(nil, facts, nil); err != nil {
		t.Fatalf("save facts: %v", err)
	}
	return dir
}

// TestKafkaTopics：三分类正确——内部产内消 / 内部产外消 / 外部产内消。
func TestKafkaTopics(t *testing.T) {
	dir := seedKafkaTopicsRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := kafkaTopics(sqlite.NewRepo(db))
	if err != nil {
		t.Fatalf("kafkaTopics: %v", err)
	}
	got := map[string]kafkaTopicCategory{}
	for _, f := range res.Topics {
		got[f.Topic] = f.Category
	}
	if got["order.created"] != kafkaCatInternal {
		t.Errorf("order.created = %q; want internal（内部产内消）", got["order.created"])
	}
	if got["order.expired"] != kafkaCatProducedInternally {
		t.Errorf("order.expired = %q; want produced-internally（内部产外消）", got["order.expired"])
	}
	if got["payment.result"] != kafkaCatConsumedInternally {
		t.Errorf("payment.result = %q; want consumed-internally（外部产内消）", got["payment.result"])
	}
	// 调用点带位置
	for _, f := range res.Topics {
		if f.Topic == "order.created" {
			if len(f.Producers) != 1 || f.Producers[0].Func != "createOrder" || !strings.Contains(f.Producers[0].Loc, "app/order.go") {
				t.Errorf("order.created producers = %+v; want createOrder(app/order.go)", f.Producers)
			}
		}
	}
}

// TestCmdKafkaTopics：CLI 输出——分类中文分组 + topic 列表。
func TestCmdKafkaTopics(t *testing.T) {
	dir := seedKafkaTopicsRepo(t)
	out := captureStdout(func() {
		if code := cmdKafkaTopics(dir, queryFlags{}); code != 0 {
			t.Errorf("cmdKafkaTopics = %d; want 0", code)
		}
	})
	for _, want := range []string{"[内部生产，内部消费]", "order.created", "[内部生产，外部消费]", "order.expired", "[外部生产，内部消费]", "payment.result"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应含 %q:\n%s", want, out)
		}
	}
}
