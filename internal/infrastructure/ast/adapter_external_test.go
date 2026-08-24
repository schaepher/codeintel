package ast

// R36 外部依赖测试：redis（方法式 + 命令式）→ redis_key 节点 +
// redis_call 边；kafka（producer/consumer）→ kafka_topic 节点 + 边。
// 测试先行（fixture 用 replace 本地 stub 模拟真实包路径）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestRedisCallEdges：redis 方法式（client.Get/Set）+ 命令式
// （conn.Do("BLPOP", key)——常量传播）→ redis_key 节点 + redis_call 边
// （写标志）。
func TestRedisCallEdges(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": `module example.com/mtest

go 1.21

require github.com/go-redis/redis/v8 v8.11.5

replace github.com/go-redis/redis/v8 => ./redistub
`,
		"redistub/go.mod": "module github.com/go-redis/redis/v8\n\ngo 1.21\n",
		"redistub/redis.go": `package redis

type Client struct{}

func (c *Client) Get(key string) (string, error) { return "", nil }
func (c *Client) Set(key string, val string) error { return nil }

type Conn struct{}

func (c *Conn) Do(args ...interface{}) (interface{}, error) { return nil, nil }
`,
		"svc/cache.go": `package svc

import "github.com/go-redis/redis/v8"

const orderKey = "order:list"

func readCache(c *redis.Client) {
	c.Get(orderKey)
}

func writeCache(c *redis.Client) {
	c.Set("user:profile", "x")
}

func popQueue(conn *redis.Conn) {
	conn.Do("BLPOP", "queue:task", 0)
}
`,
	})
	// redis_call 边断言（3 条：Get 常量键/Set 字面量键/Do 命令式）
	seen := map[string]bool{}
	for _, f := range facts {
		if f.Kind != domain.FactRedisCall {
			continue
		}
		key, _ := f.Metadata["key"].(string)
		write, _ := f.Metadata["write"].(bool)
		seen[key] = true
		if key == "order:list" && write {
			t.Error("Get 应为读")
		}
		if key == "user:profile" && !write {
			t.Error("Set 应为写")
		}
	}
	for _, want := range []string{"order:list", "user:profile", "queue:task"} {
		if !seen[want] {
			t.Errorf("缺 redis 键 %s（方法式/命令式识别）:\n%v", want, facts)
		}
	}
}

// TestKafkaTopicEdges：sarama producer.SendMessage(&ProducerMessage{
// Topic}) + consumer.ConsumePartition(topic) → kafka_topic 节点 +
// kafka_produce/kafka_consume 边。
func TestKafkaTopicEdges(t *testing.T) {
	nodes, facts := indexFixture(t, map[string]string{
		"go.mod": `module example.com/mtest

go 1.21

require github.com/IBM/sarama v1.40.0

replace github.com/IBM/sarama => ./kafkastub
`,
		"kafkastub/go.mod": "module github.com/IBM/sarama\n\ngo 1.21\n",
		"kafkastub/kafka.go": `package sarama

type ProducerMessage struct {
	Topic string
	Value []byte
}

type SyncProducer interface {
	SendMessage(msg *ProducerMessage) (int32, int64, error)
}

type Consumer interface {
	ConsumePartition(topic string, partition int32, offset int64) (interface{}, error)
}
`,
		"svc/queue.go": `package svc

import "github.com/IBM/sarama"

func sendOrder(p sarama.SyncProducer) {
	p.SendMessage(&sarama.ProducerMessage{Topic: "order.created"})
}

func consumeOrder(c sarama.Consumer) {
	c.ConsumePartition("order.created", 0, 0)
}

func sendPayment(p sarama.SyncProducer) {
	p.SendMessage(&sarama.ProducerMessage{Topic: "payment.done"})
}
`,
	})
	topicNode := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == domain.KindKafkaTopic {
			topicNode[n.Name] = true
		}
	}
	producers := map[string]bool{}
	consumers := map[string]bool{}
	for _, f := range facts {
		topic, _ := f.Metadata["topic"].(string)
		switch f.Kind {
		case domain.FactKafkaProduce:
			producers[topic] = true
		case domain.FactKafkaConsume:
			consumers[topic] = true
		}
	}
	for _, want := range []string{"order.created", "payment.done"} {
		if !topicNode[want] {
			t.Errorf("缺 kafka_topic 节点 %s", want)
		}
	}
	if !producers["order.created"] || !consumers["order.created"] {
		t.Errorf("order.created 应既有 producer 又有 consumer（双向）")
	}
	if !producers["payment.done"] || consumers["payment.done"] {
		t.Errorf("payment.done 应只有 producer")
	}
}
