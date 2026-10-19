package scip

// todo5-1：逐文档解码 SCIP index（不一次物化全部 protobuf 文档与
// occurrence）。使用 protobuf 官方的 protowire（已在依赖内，不新增依赖）
// 逐个消费顶层字段：
//   - documents(2)：单独 Unmarshal 一个 Document → 处理 → 立即释放
//   - metadata(1) / external_symbols(3)：仍完整解码校验（保留字段语义）
//   - 其它字段：按 wire 格式校验后跳过（与 proto.Unmarshal 语义一致）
//
// 截断/非法长度/非法嵌套消息返回错误——调用方不得提交部分成功构建；
// fn 输出错误与 ctx 取消原样传播。

import (
	"context"
	"fmt"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// scip Index 顶层字段号。
const (
	fieldMetadata        = 1
	fieldDocuments       = 2
	fieldExternalSymbols = 3
)

// forEachIndexDocument 逐文档遍历 SCIP Index 字节流。
func forEachIndexDocument(ctx context.Context, data []byte, fn func(*scip.Document) error) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return fmt.Errorf("scip index: invalid tag: %w", protowire.ParseError(n))
		}
		data = data[n:]
		if typ == protowire.BytesType &&
			(num == fieldMetadata || num == fieldDocuments || num == fieldExternalSymbols) {
			b, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return fmt.Errorf("scip index field %d: %w", num, protowire.ParseError(n))
			}
			data = data[n:]
			switch num {
			case fieldMetadata:
				var m scip.Metadata
				if err := proto.Unmarshal(b, &m); err != nil {
					return fmt.Errorf("scip index metadata: %w", err)
				}
			case fieldDocuments:
				doc := &scip.Document{}
				if err := proto.Unmarshal(b, doc); err != nil {
					return fmt.Errorf("scip index document: %w", err)
				}
				if err := fn(doc); err != nil {
					return err
				}
			case fieldExternalSymbols:
				var si scip.SymbolInformation
				if err := proto.Unmarshal(b, &si); err != nil {
					return fmt.Errorf("scip index external symbol: %w", err)
				}
			}
			continue
		}
		// 非 bytes / 未知字段：完整消费（合法性检查）
		n = protowire.ConsumeFieldValue(num, typ, data)
		if n < 0 {
			return fmt.Errorf("scip index field %d: %w", num, protowire.ParseError(n))
		}
		data = data[n:]
	}
	return nil
}
