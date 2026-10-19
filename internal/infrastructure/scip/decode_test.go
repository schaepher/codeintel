package scip

import (
	"context"
	"errors"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/schaepher/codeintel/internal/domain"
)

func sampleIndex() *scip.Index {
	return &scip.Index{
		Metadata: &scip.Metadata{},
		Documents: []*scip.Document{
			{
				RelativePath: "svc/svc.go",
				Symbols: []*scip.SymbolInformation{{
					Symbol: "scip-go gomod example.com/m . `example.com/m/svc`/Service#",
					Kind:   scip.SymbolInformation_Struct,
				}},
				Occurrences: []*scip.Occurrence{{
					Symbol:      "scip-go gomod example.com/m . `example.com/m/svc`/Service#",
					SymbolRoles: int32(scip.SymbolRole_Definition),
					Range:       []int32{3, 0, 7},
				}},
			},
			{RelativePath: "other/other.go"},
		},
		ExternalSymbols: []*scip.SymbolInformation{{Symbol: "ext"}},
	}
}

// TestForEachIndexDocumentMatchesUnmarshal（todo5-1）：逐文档解码与原
// proto.Unmarshal 结果逐文档 proto.Equal。
func TestForEachIndexDocumentMatchesUnmarshal(t *testing.T) {
	idx := sampleIndex()
	data, err := proto.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	var got []*scip.Document
	if err := forEachIndexDocument(context.Background(), data, func(d *scip.Document) error {
		got = append(got, d)
		return nil
	}); err != nil {
		t.Fatalf("forEachIndexDocument: %v", err)
	}
	if len(got) != len(idx.Documents) {
		t.Fatalf("documents = %d, want %d", len(got), len(idx.Documents))
	}
	for i := range got {
		if !proto.Equal(got[i], idx.Documents[i]) {
			t.Errorf("doc[%d] proto.Equal = false", i)
		}
	}
}

// TestForEachIndexDocumentExtraFields（todo5-1）：未知/额外字段按 wire
// 校验后跳过，文档仍逐项一致。
func TestForEachIndexDocumentExtraFields(t *testing.T) {
	data, err := proto.Marshal(sampleIndex())
	if err != nil {
		t.Fatal(err)
	}
	extra := protowire.AppendTag(nil, 99, protowire.VarintType)
	extra = protowire.AppendVarint(extra, 42)
	data = append(append([]byte(nil), data...), extra...)
	n := 0
	if err := forEachIndexDocument(context.Background(), data, func(*scip.Document) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("额外字段应跳过: %v", err)
	}
	if n != 2 {
		t.Errorf("documents = %d, want 2", n)
	}
}

// TestForEachIndexDocumentRejectsBadInput（todo5-1）：截断、非法长度、
// 非法嵌套消息都必须返回失败（不静默部分成功）。
func TestForEachIndexDocumentRejectsBadInput(t *testing.T) {
	good, err := proto.Marshal(sampleIndex())
	if err != nil {
		t.Fatal(err)
	}
	// 截断
	if err := forEachIndexDocument(context.Background(), good[:len(good)-3], func(*scip.Document) error { return nil }); err == nil {
		t.Error("截断输入应返回错误")
	}
	// 非法长度（声明 10 字节只有 3 字节）
	badLen := protowire.AppendTag(nil, fieldDocuments, protowire.BytesType)
	badLen = protowire.AppendVarint(badLen, 10)
	badLen = append(badLen, 1, 2, 3)
	if err := forEachIndexDocument(context.Background(), badLen, func(*scip.Document) error { return nil }); err == nil {
		t.Error("非法长度应返回错误")
	}
	// 非法嵌套消息（Document 内非法 tag）
	badMsg := protowire.AppendTag(nil, fieldDocuments, protowire.BytesType)
	badMsg = protowire.AppendBytes(badMsg, []byte{0xFF, 0xFF})
	if err := forEachIndexDocument(context.Background(), badMsg, func(*scip.Document) error { return nil }); err == nil {
		t.Error("非法嵌套消息应返回错误")
	}
}

// TestForEachIndexDocumentPropagatesOutputAndCancel（todo5-1）：输出错误
// 与 ctx 取消原样传播。
func TestForEachIndexDocumentPropagatesOutputAndCancel(t *testing.T) {
	data, err := proto.Marshal(sampleIndex())
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("emit fail")
	if err := forEachIndexDocument(context.Background(), data, func(*scip.Document) error {
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Errorf("输出错误未传播: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := forEachIndexDocument(ctx, data, func(*scip.Document) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("取消未传播: %v", err)
	}
}

// TestProcessDocumentStreamingEqualsUnmarshal（todo5-1）：规范化产物
// （节点/边）与旧解码路径一致。
func TestProcessDocumentStreamingEqualsUnmarshal(t *testing.T) {
	idx := sampleIndex()
	data, err := proto.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	repo := &domain.Repository{Module: "example.com/m", Modules: []string{"example.com/m"}}
	adapter := &Adapter{}
	run := func(docs []*scip.Document) ([]string, []string) {
		var nodes, facts []string
		emit := func(item domain.Item) error {
			if item.Node != nil {
				nodes = append(nodes, string(item.Node.ID))
			}
			if item.Fact != nil {
				facts = append(facts, string(item.Fact.SourceID)+"->"+string(item.Fact.TargetID))
			}
			return nil
		}
		for _, d := range docs {
			if err := adapter.processDocument(repo, d, ".", emit); err != nil {
				t.Fatalf("processDocument: %v", err)
			}
		}
		return nodes, facts
	}
	oldNodes, oldFacts := run(idx.Documents)
	var streamed []*scip.Document
	if err := forEachIndexDocument(context.Background(), data, func(d *scip.Document) error {
		streamed = append(streamed, d)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	newNodes, newFacts := run(streamed)
	if len(oldNodes) != len(newNodes) {
		t.Fatalf("节点数不一致: %d vs %d", len(oldNodes), len(newNodes))
	}
	for i := range oldNodes {
		if oldNodes[i] != newNodes[i] {
			t.Errorf("node[%d] = %s, want %s", i, newNodes[i], oldNodes[i])
		}
	}
	if len(oldFacts) != len(newFacts) {
		t.Fatalf("边数不一致: %d vs %d", len(oldFacts), len(newFacts))
	}
	for i := range oldFacts {
		if oldFacts[i] != newFacts[i] {
			t.Errorf("fact[%d] = %s, want %s", i, newFacts[i], oldFacts[i])
		}
	}
}
