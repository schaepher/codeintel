package sqlite

// 节点属性序列化（P1-04：写库热路径的重复分配治理）。
//
// 原实现 `json.Marshal(props)` 每个节点写盘一次：返回的 []byte 与随后的
// `string(props)` 各分配一次；nil/空属性（go2o 实测 1855 个节点）也照样
// 走一遍编码器。这里改为：
//   - 空/nil 属性 → 共享常量 "{}"（json_patch 需要对象操作数），零编码
//   - 非空属性 → 池化 bytes.Buffer + json.Encoder，只保留一次字符串拷贝
//     （避免 json.Marshal 的中间 []byte）
//
// 语义保持：json.Encoder 的默认选项与 json.Marshal 一致（HTML 转义、
// 确定性键序），Encode 额外追加的 '\n' 被裁掉——与 json.Marshal 输出
// **逐字节相等**，由 marshal_props_test.go 的形态矩阵锁定。
//
// 为什么不做"内容缓存"：节点属性每个节点独立构造，最终库只有约 13%
// 属性和已有节点重复（等值节点已由 orchestrator 的 node_batch 跳过），
// 而按内容判定等值本身就要先编码，收益低于成本（2026-10 实测）。这里
// 只消除重复分配，不引入内容键缓存。

import (
	"bytes"
	"encoding/json"
	"sync"

	"go.uber.org/zap"
)

// emptyPropsJSON 空属性对象（只读共享；调用方拿到的是拷贝后的 string）。
const emptyPropsJSON = "{}"

// propsEncoder 池化的编码器（buffer 与 encoder 绑定，Reset 复用）。
type propsEncoder struct {
	buf bytes.Buffer
	enc *json.Encoder
}

var propsEncoderPool = sync.Pool{New: func() any {
	p := &propsEncoder{}
	p.enc = json.NewEncoder(&p.buf)
	return p
}}

// marshalPropsJSON 序列化节点属性为 JSON 字符串；nil/空映射为空对象。
func marshalPropsJSON(props map[string]any) (string, error) {
	logger := zap.L()
	logger.Debug("enter marshalPropsJSON")
	defer logger.Debug("exit marshalPropsJSON")
	if len(props) == 0 {
		return emptyPropsJSON, nil
	}
	p := propsEncoderPool.Get().(*propsEncoder)
	p.buf.Reset()
	err := p.enc.Encode(props)
	if err != nil {
		propsEncoderPool.Put(p)
		return "", err
	}
	b := p.buf.Bytes()
	// Encode 追加一个 '\n'；json.Marshal 不追加（见文件头语义说明）。
	b = b[:len(b)-1]
	out := string(b) // 唯一一次分配（buffer 归还池后不可再引用）
	propsEncoderPool.Put(p)
	return out, nil
}
