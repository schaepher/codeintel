package ast

// R36 外部依赖分析（待办 5）：
// - redis：方法式（go-redis client.Get/Set/HSet...——接收者类型
//   *redis.Client/*redis.Conn）与命令式（conn.Do("GET", key)——Do 第 1
//   参命令名、第 2 参键）→ redis_key 节点 + redis_call 边（键名
//   字面量/常量传播——extractStringArg）
// - kafka（sarama）：SendMessage(&ProducerMessage{Topic:"x"}) 生产者 +
//   ConsumePartition(topic, ...) 消费者 → kafka_topic 节点 +
//   kafka_produce/kafka_consume 边
// 键/topic 是外部存储的"表"——value-trace 可追。

import (
	"go/ast"
	"go/constant"
	"go/types"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// redisReadCmds redis 读命令（方法式 Get/命令式 GET 等——写命令单独
// 归写）。
var redisReadCmds = map[string]bool{
	"Get": true, "MGet": true, "Exists": true, "TTL": true, "HGet": true,
	"HGetAll": true, "SMembers": true, "SCard": true, "LLen": true,
	"LRange": true, "ZRange": true, "GET": true, "EXISTS": true,
	"KEYS": true, "HGET": true, "HGETALL": true, "SMEMBERS": true,
	"LRANGE": true, "BLPOP": true,
}

// redisWriteCmds redis 写命令。
var redisWriteCmds = map[string]bool{
	"Set": true, "MSet": true, "Del": true, "Expire": true, "HSet": true,
	"SAdd": true, "LPush": true, "RPush": true, "Incr": true, "Decr": true,
	"SET": true, "DEL": true, "EXPIRE": true, "HSET": true, "SADD": true,
	"LPUSH": true, "RPUSH": true, "INCR": true, "INCRBY": true,
}

// isRedisClient 类型是 redis 客户端（go-redis/redigo——方法式接收者）。
// redigo 的 redis.Conn/Client 是**接口**（go2o 实测）——Named 与
// Interface 都处理。
func isRedisClient(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	switch tt := t.(type) {
	case *types.Named:
		if tt.Obj().Pkg() == nil {
			return false
		}
		p := tt.Obj().Pkg().Path()
		return strings.Contains(p, "go-redis/redis") || strings.Contains(p, "gomodule/redigo") ||
			strings.Contains(p, "redis/go-redis") ||
			(tt.Obj().Name() == "Conn" && strings.Contains(p, "redis"))
	case *types.Interface:
		// redigo redis.Conn（接口）——包路径判断
		if tt.NumExplicitMethods() == 0 {
			return false
		}
		// 从方法签名找包路径（Do 方法的接收者包）
		for i := 0; i < tt.NumExplicitMethods(); i++ {
			m := tt.ExplicitMethod(i)
			if m.Name() == "Do" || m.Name() == "Get" {
				if sig, ok := m.Type().(*types.Signature); ok && sig.Recv() != nil {
					return isRedisClient(sig.Recv().Type())
				}
			}
		}
		return false
	}
	return false
}

// redisCmdName 调用方法名（Do 命令式取第 1 参命令名；方法式取方法名）。
func redisCmdName(pkg *packages.Package, call *ast.CallExpr, method string, methodVars map[string]string) (string, bool) {
	if method != "Do" && method != "DoContext" {
		return method, redisReadCmds[method] || redisWriteCmds[method]
	}
	if len(call.Args) < 1 {
		return "", false
	}
	cmd := extractStringArg(pkg, methodVars, call.Args[0])
	if cmd == "" {
		return "", false
	}
	return cmd, redisReadCmds[cmd] || redisWriteCmds[cmd]
}

// redisKeyArg redis 调用第 1 参（键——方法式 key 参数 / 命令式第 2 参）。
// 支持字面量/同函数常量（extractStringArg）+ **跨包常量**
// （constants.QueueNewMailTask——go2o 实测形态，types.Const 取值）。
func redisKeyArg(pkg *packages.Package, call *ast.CallExpr, method string, methodVars map[string]string) string {
	idx := 0
	if method == "Do" || method == "DoContext" {
		idx = 1
	}
	if len(call.Args) <= idx {
		return ""
	}
	arg := call.Args[idx]
	if v := extractStringArg(pkg, methodVars, arg); v != "" {
		return v
	}
	// 跨包常量：constants.QueueNewMailTask（SelectorExpr → types.Const）
	if sel, ok := arg.(*ast.SelectorExpr); ok {
		if obj := pkg.TypesInfo.ObjectOf(sel.Sel); obj != nil {
			if c, ok := obj.(*types.Const); ok {
				return constant.StringVal(c.Val())
			}
		}
	}
	return ""
}

// emitRedisCall 识别 redis 调用（x.Get(key)/conn.Do("GET", key)）→
// redis_key 节点 + redis_call 边。
func (ctx *fileCtx) emitRedisCall(call *ast.CallExpr, callee *types.Func, sel *ast.SelectorExpr,
	xid *ast.Ident, callerID domain.CanonicalID) {
	if callee == nil || callee.Pkg() == nil || !isRedisClient(ctx.pkg.TypesInfo.TypeOf(xid)) {
		return
	}
	cmd, known := redisCmdName(ctx.pkg, call, callee.Name(), ctx.methodVars)
	if !known {
		return
	}
	key := redisKeyArg(ctx.pkg, call, callee.Name(), ctx.methodVars)
	if key == "" {
		return
	}
	isWrite := redisWriteCmds[cmd]
	keyID := domain.CanonicalID("symbol:redis:" + key)
	_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
		ID: keyID, Kind: domain.KindRedisKey, Name: key,
		Properties: map[string]any{"cmd": cmd, "write": boolStr(isWrite)},
	}})
	_ = ctx.emit(domain.Item{Fact: &domain.Fact{
		SourceID: callerID, TargetID: keyID,
		Kind:       domain.FactRedisCall,
		ToolSource: domain.ToolCodeGraph,
		Confidence: 1.0,
		Metadata: map[string]any{
			"cmd":      cmd,
			"key":      key,
			"write":    isWrite,
			"line_num": ctx.pkg.Fset.PositionFor(call.Pos(), false).Line,
		},
	}})
}

// isSaramaProducer 类型是 sarama producer（SyncProducer/AsyncProducer）。
func isSaramaProducer(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	if n.Obj().Pkg() == nil || !strings.Contains(n.Obj().Pkg().Path(), "sarama") {
		return false
	}
	return n.Obj().Name() == "SyncProducer" || n.Obj().Name() == "AsyncProducer"
}

// isSaramaConsumer 类型是 sarama consumer（Consumer/ConsumerGroup）。
func isSaramaConsumer(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	if n.Obj().Pkg() == nil || !strings.Contains(n.Obj().Pkg().Path(), "sarama") {
		return false
	}
	return n.Obj().Name() == "Consumer" || n.Obj().Name() == "ConsumerGroup" ||
		n.Obj().Name() == "PartitionConsumer"
}

// kafkaTopicArg 从调用参数提取 topic（SendMessage 的 ProducerMessage
// Topic 字段 / ConsumePartition 第 1 参）。
func kafkaTopicArg(pkg *packages.Package, call *ast.CallExpr, method string, methodVars map[string]string) string {
	switch method {
	case "SendMessage", "SendMessages":
		if len(call.Args) < 1 {
			return ""
		}
		// &ProducerMessage{Topic: "x"} 或 ProducerMessage{Topic: "x"}
		lit := call.Args[0]
		if u, ok := lit.(*ast.UnaryExpr); ok {
			lit = u.X
		}
		cl, ok := lit.(*ast.CompositeLit)
		if !ok {
			return ""
		}
		for _, el := range cl.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Topic" {
				return extractStringArg(pkg, methodVars, kv.Value)
			}
		}
		return ""
	case "ConsumePartition", "Consume":
		if len(call.Args) < 1 {
			return ""
		}
		return extractStringArg(pkg, methodVars, call.Args[0])
	}
	return ""
}

// emitKafkaCall 识别 kafka 调用（producer.SendMessage → topic /
// consumer.ConsumePartition(topic)）→ kafka_topic 节点 + 边。
func (ctx *fileCtx) emitKafkaCall(call *ast.CallExpr, callee *types.Func, sel *ast.SelectorExpr,
	xid *ast.Ident, callerID domain.CanonicalID) {
	if callee == nil || callee.Pkg() == nil {
		return
	}
	rt := ctx.pkg.TypesInfo.TypeOf(xid)
	topic := kafkaTopicArg(ctx.pkg, call, callee.Name(), ctx.methodVars)
	if topic == "" {
		return
	}
	var kind domain.FactKind
	switch {
	case isSaramaProducer(rt) && (callee.Name() == "SendMessage" || callee.Name() == "SendMessages"):
		kind = domain.FactKafkaProduce
	case isSaramaConsumer(rt) && (callee.Name() == "ConsumePartition" || callee.Name() == "Consume"):
		kind = domain.FactKafkaConsume
	default:
		return
	}
	topicID := domain.CanonicalID("symbol:kafka:" + topic)
	_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
		ID: topicID, Kind: domain.KindKafkaTopic, Name: topic,
	}})
	_ = ctx.emit(domain.Item{Fact: &domain.Fact{
		SourceID: callerID, TargetID: topicID,
		Kind:       kind,
		ToolSource: domain.ToolCodeGraph,
		Confidence: 1.0,
		Metadata: map[string]any{
			"topic":    topic,
			"line_num": ctx.pkg.Fset.PositionFor(call.Pos(), false).Line,
		},
	}})
}

// boolStr bool → "true"/"false"（节点属性字符串化）。
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
