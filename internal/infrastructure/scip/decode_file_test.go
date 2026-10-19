package scip

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func writeScipFile(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func collectDocs(t *testing.T, data []byte, file bool) ([]*scip.Document, error) {
	t.Helper()
	var docs []*scip.Document
	cb := func(d *scip.Document) error { docs = append(docs, d); return nil }
	var err error
	if file {
		err = forEachIndexFileDocument(context.Background(), writeScipFile(t, data), cb)
	} else {
		err = forEachIndexDocument(context.Background(), data, cb)
	}
	return docs, err
}

func assertDocsEqual(t *testing.T, a, b []*scip.Document) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("文档数不一致: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			t.Fatalf("文档[%d]不一致:\n%v\n%v", i, a[i], b[i])
		}
	}
}

// TestForEachIndexFileMatchesSlice：文件流式与切片版逐文档一致
// （含未知字段 varint/bytes/fixed32/fixed64/group）。
func TestForEachIndexFileMatchesSlice(t *testing.T) {
	base, err := proto.Marshal(sampleIndex())
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Clone(base)
	unknown = protowire.AppendTag(unknown, 9, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 42)
	unknown = protowire.AppendTag(unknown, 10, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, []byte("skip"))
	unknown = protowire.AppendTag(unknown, 11, protowire.Fixed32Type)
	unknown = protowire.AppendFixed32(unknown, 7)
	unknown = protowire.AppendTag(unknown, 12, protowire.Fixed64Type)
	unknown = protowire.AppendFixed64(unknown, 8)
	unknown = protowire.AppendTag(unknown, 13, protowire.StartGroupType)
	unknown = protowire.AppendTag(unknown, 14, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	unknown = protowire.AppendTag(unknown, 13, protowire.EndGroupType)
	// 也把未知字段插到文档之间（在流式路径里需继续解析）
	mid := append([]byte{}, base[:5]...)
	mid = append(mid, unknown[5:]...)
	for name, data := range map[string][]byte{
		"base": base, "unknown": unknown, "unknown-mid": mid,
	} {
		t.Run(name, func(t *testing.T) {
			want, wantErr := collectDocs(t, data, false)
			got, gotErr := collectDocs(t, data, true)
			if (wantErr != nil) != (gotErr != nil) {
				t.Fatalf("错误不一致: slice=%v file=%v", wantErr, gotErr)
			}
			if wantErr == nil {
				assertDocsEqual(t, want, got)
			}
		})
	}
}

// TestForEachIndexFileErrors：空文件正常结束；截断/非法长度/非法嵌套报错。
func TestForEachIndexFileErrors(t *testing.T) {
	if docs, err := collectDocs(t, nil, true); err != nil || len(docs) != 0 {
		t.Fatalf("空文件应正常结束: docs=%d err=%v", len(docs), err)
	}
	good, err := proto.Marshal(sampleIndex())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"truncated": good[:len(good)-3],
		"bad-tag":   {0xFF, 0xFF},
	}
	badLen := protowire.AppendTag(nil, fieldDocuments, protowire.BytesType)
	badLen = protowire.AppendVarint(badLen, 10)
	badLen = append(badLen, 1, 2, 3)
	cases["bad-length"] = badLen
	badMsg := protowire.AppendTag(nil, fieldDocuments, protowire.BytesType)
	badMsg = protowire.AppendBytes(badMsg, []byte{0xFF, 0xFF})
	cases["bad-message"] = badMsg
	for name, data := range cases {
		if _, err := collectDocs(t, data, true); err == nil {
			t.Errorf("%s 应返回错误", name)
		}
	}
}

// TestForEachIndexFileCancelAndOutput：输出错误与 ctx 取消传播。
func TestForEachIndexFileCancelAndOutput(t *testing.T) {
	data, err := proto.Marshal(sampleIndex())
	if err != nil {
		t.Fatal(err)
	}
	path := writeScipFile(t, data)
	sentinel := &testError{}
	if err := forEachIndexFileDocument(context.Background(), path, func(*scip.Document) error {
		return sentinel
	}); err != sentinel {
		t.Fatalf("输出错误未原样传播: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := forEachIndexFileDocument(ctx, path, func(*scip.Document) error { return nil }); err != context.Canceled {
		t.Fatalf("取消未传播: %v", err)
	}
}

type testError struct{}

func (*testError) Error() string { return "emit fail" }

// TestForEachIndexFileLargeDocument：大文档（>64KB 读缓冲）仍正确。
func TestForEachIndexFileLargeDocument(t *testing.T) {
	big := strings.Repeat("a", 300<<10)
	data, err := proto.Marshal(&scip.Index{Documents: []*scip.Document{{RelativePath: "big.go", Text: big}}})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := collectDocs(t, data, true)
	if err != nil || len(docs) != 1 || docs[0].Text != big {
		t.Fatalf("大文档解析失败: docs=%d err=%v", len(docs), err)
	}
}
