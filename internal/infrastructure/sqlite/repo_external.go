package sqlite

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// R94：外部依赖（redis/kafka）与外部接口调用（query external-*）窄接口
// ——action 层 Reader 实现。redis_key/kafka_topic 节点 + 调用边
// （redis_call/kafka_produce/kafka_consume/grpc_call/http_call，metadata
// 全量）。

// GetRedisKeyNodes kind='redis_key' 节点（含 properties.write/cmd——
// R36 发射器写入）。
func (r *Repo) GetRedisKeyNodes() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetRedisKeyNodes")
	defer logger.Debug("exit (Repo).GetRedisKeyNodes")
	rows, err := r.Query(`SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE kind = 'redis_key'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// GetKafkaTopicNodes kind='kafka_topic' 节点（R36 发射器写入）。
func (r *Repo) GetKafkaTopicNodes() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetKafkaTopicNodes")
	defer logger.Debug("exit (Repo).GetKafkaTopicNodes")
	rows, err := r.Query(`SELECT id, kind, name, file_path, line_start, line_end, properties
		FROM nodes WHERE kind = 'kafka_topic'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// GetFactsByKinds 指定 kind 的调用边（metadata 全量反序列化——外部
// 调用聚合用：redis_call/kafka_produce/kafka_consume/grpc_call/http_call）。
func (r *Repo) GetFactsByKinds(kinds ...string) ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetFactsByKinds", zap.Strings("kinds", kinds))
	defer logger.Debug("exit (Repo).GetFactsByKinds")
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	args := make([]any, len(kinds))
	for i, k := range kinds {
		args[i] = k
	}
	rows, err := r.Query(`SELECT source_id, target_id, kind, tool_source, confidence, metadata
		FROM edges WHERE kind IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFacts(rows)
}
