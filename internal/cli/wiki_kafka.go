package cli

// R47 kafka topic 分类 wiki 节（用户要求独立一节）：按生产/消费归属
// 三分类分组展示（内部生产内部消费 / 内部生产外部消费 / 外部生产
// 内部消费），每 topic 带生产/消费调用点。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// renderKafkaTopicsMD kafka topic 节（md——R47；无 topic 返回空）。
func renderKafkaTopicsMD(repo *sqlite.Repo) string {
	res, err := kafkaTopics(repo)
	if err != nil || len(res.Topics) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Kafka Topic\n\n")
	b.WriteString("> 数据源：kafka_topic 节点 + kafka_produce/kafka_consume 边——按生产/消费归属分类（外部系统消息集成点）。\n\n")
	cur := ""
	for _, t := range res.Topics {
		if t.Category.cn() != cur {
			cur = t.Category.cn()
			b.WriteString("### " + cur + "\n\n")
		}
		b.WriteString("- `" + t.Topic + "`")
		if len(t.Producers) > 0 {
			b.WriteString("（生产：" + joinKafkaCallers(t.Producers) + "）")
		}
		if len(t.Consumers) > 0 {
			b.WriteString("（消费：" + joinKafkaCallers(t.Consumers) + "）")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// renderKafkaTopicsHTML kafka topic 节（html——R47）。
func renderKafkaTopicsHTML(repo *sqlite.Repo) string {
	res, err := kafkaTopics(repo)
	if err != nil || len(res.Topics) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="kafka"><h2>Kafka Topic</h2><p class="muted">数据源：kafka_topic 节点 + kafka_produce/kafka_consume 边——按生产/消费归属分类（外部系统消息集成点）。</p>`)
	cur := ""
	for _, t := range res.Topics {
		if t.Category.cn() != cur {
			cur = t.Category.cn()
			b.WriteString(fmt.Sprintf(`<h3>%s</h3><ul>`, htmlEsc(cur)))
		}
		b.WriteString("<li><code>" + htmlEsc(t.Topic) + "</code>")
		if len(t.Producers) > 0 {
			b.WriteString("（生产：" + htmlEsc(joinKafkaCallers(t.Producers)) + "）")
		}
		if len(t.Consumers) > 0 {
			b.WriteString("（消费：" + htmlEsc(joinKafkaCallers(t.Consumers)) + "）")
		}
		b.WriteString("</li>")
	}
	b.WriteString("</ul></section>")
	return b.String()
}
