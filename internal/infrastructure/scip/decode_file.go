package scip

// todo9-3：SCIP index 从文件流式读取（不回读整个 index 文件）。
// go2o 的 index 约 30MB，原 os.ReadFile + forEachIndexDocument 一次性
// 分配整个文件；这里用 bufio + protowire 逐字段消费，文档内容仍逐文档
// Unmarshal（todo5-1 的内存性质），metadata/external_symbols 仍完整解码
// 保留合法性检查。wire 语义与 forEachIndexDocument（切片版）一致。

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// maxScipFieldSize 单字段长度上限（防损坏长度导致巨额分配）。
const maxScipFieldSize = 256 << 20

// forEachIndexFileDocument 文件版 forEachIndexDocument（逐字段流式）。
func forEachIndexFileDocument(ctx context.Context, path string, fn func(*scip.Document) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return forEachIndexReaderDocument(ctx, bufio.NewReaderSize(f, 64<<10), fn)
}

// forEachIndexReaderDocument 从 reader 逐字段解析 SCIP Index。
func forEachIndexReaderDocument(ctx context.Context, r *bufio.Reader, fn func(*scip.Document) error) error {
	var scratch []byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tag, err := binary.ReadUvarint(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("scip index: invalid tag: %w", err)
		}
		num, typ := protowire.DecodeTag(tag)
		if num < protowire.MinValidNumber {
			return fmt.Errorf("scip index: invalid tag: invalid field number %d", num)
		}
		if typ == protowire.BytesType &&
			(num == fieldMetadata || num == fieldDocuments || num == fieldExternalSymbols) {
			if scratch, err = readFieldBytes(r, scratch); err != nil {
				return fmt.Errorf("scip index field %d: %w", num, err)
			}
			switch num {
			case fieldMetadata:
				var m scip.Metadata
				if err := proto.Unmarshal(scratch, &m); err != nil {
					return fmt.Errorf("scip index metadata: %w", err)
				}
			case fieldDocuments:
				doc := &scip.Document{}
				if err := proto.Unmarshal(scratch, doc); err != nil {
					return fmt.Errorf("scip index document: %w", err)
				}
				if err := fn(doc); err != nil {
					return err
				}
			case fieldExternalSymbols:
				var si scip.SymbolInformation
				if err := proto.Unmarshal(scratch, &si); err != nil {
					return fmt.Errorf("scip index external symbol: %w", err)
				}
			}
			continue
		}
		if err := skipFieldValue(r, num, typ, protowire.DefaultRecursionLimit); err != nil {
			return fmt.Errorf("scip index field %d: %w", num, err)
		}
	}
}

// readFieldBytes 读 length-delimited 字段（复用 scratch 缓冲）。
func readFieldBytes(r *bufio.Reader, scratch []byte) ([]byte, error) {
	ln, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if ln > maxScipFieldSize {
		return nil, fmt.Errorf("field too large: %d bytes", ln)
	}
	n := int(ln)
	if cap(scratch) < n {
		scratch = make([]byte, n)
	} else {
		scratch = scratch[:n]
	}
	if _, err := io.ReadFull(r, scratch); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return scratch, nil
}

// skipFieldValue 按 wire 类型消费并丢弃字段值（与 protowire.ConsumeFieldValue
// 语义一致：未知字段跳过、group 校验 end-group、非法类型报错）。
func skipFieldValue(r *bufio.Reader, num protowire.Number, typ protowire.Type, depth int) error {
	switch typ {
	case protowire.VarintType:
		_, err := binary.ReadUvarint(r)
		return err
	case protowire.Fixed32Type:
		_, err := io.CopyN(io.Discard, r, 4)
		return err
	case protowire.Fixed64Type:
		_, err := io.CopyN(io.Discard, r, 8)
		return err
	case protowire.BytesType:
		ln, err := binary.ReadUvarint(r)
		if err != nil {
			return err
		}
		if ln > maxScipFieldSize {
			return fmt.Errorf("field too large: %d bytes", ln)
		}
		_, err = io.CopyN(io.Discard, r, int64(ln))
		return err
	case protowire.StartGroupType:
		if depth <= 0 {
			return fmt.Errorf("group nesting too deep")
		}
		for {
			tag, err := binary.ReadUvarint(r)
			if err != nil {
				return err
			}
			num2, typ2 := protowire.DecodeTag(tag)
			if num2 < protowire.MinValidNumber {
				return fmt.Errorf("invalid field number %d", num2)
			}
			if typ2 == protowire.EndGroupType {
				if num != num2 {
					return fmt.Errorf("mismatching end group: %d != %d", num2, num)
				}
				return nil
			}
			if err := skipFieldValue(r, num2, typ2, depth-1); err != nil {
				return err
			}
		}
	case protowire.EndGroupType:
		return fmt.Errorf("unexpected end group")
	default:
		return fmt.Errorf("invalid wire type %d", typ)
	}
}
