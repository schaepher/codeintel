package action

// 用户检查发现的 bug：lineTargets 是 map[int]string（key=行号）——
// 同一行多个函数调用（s.Save(nil); n.Notify(nil)）时两个调用边的
// line_num 相同，后写入的覆盖先写入 → 两个时序节点都拿到同一个
// target（错误展开）。修复：发射端 metadata 加 pos（字节 offset），
// 消费端 offset 优先匹配（同行可区分），行号作 fallback（兼容旧索引）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestSeqMultiCallSameLine：一行两个调用——各自命中各自的 target
// （修复前：同行覆盖，两个节点 Type 都错）。
func TestSeqMultiCallSameLine(t *testing.T) {
	src := `package m

type Saver interface{ Save(o any) }
type Notifier interface{ Notify(o any) }

func run(s Saver, n Notifier) {
	s.Save(nil); n.Notify(nil)
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// 两个调用同行（line 7）；offset 不同（修复后区分依据——与发射端
	// 一致：call.Lparen 的字节偏移）
	saveOff := strings.Index(src, "s.Save(") + len("s.Save")
	notifyOff := strings.Index(src, "n.Notify(") + len("n.Notify")
	if saveOff < 0 || notifyOff < 0 {
		t.Fatal("fixture 源码定位失败")
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:run", Kind: domain.KindFunction, Name: "run", FilePath: "main.go", LineStart: 6},
		{ID: "symbol:go:example.com/m:(Saver).Save", Kind: domain.KindMethod, Name: "(Saver).Save", FilePath: "main.go", LineStart: 4},
		{ID: "symbol:go:example.com/m:(Notifier).Notify", Kind: domain.KindMethod, Name: "(Notifier).Notify", FilePath: "main.go", LineStart: 5},
	}, []*domain.Fact{
		// 同行（line 7）两条边——pos 不同（新发射端形态）
		{SourceID: "symbol:go:example.com/m:run", TargetID: "symbol:go:example.com/m:(Saver).Save",
			Kind: domain.FactCalls, Confidence: 1.0,
			Metadata: map[string]any{"line_num": 7.0, "pos": float64(saveOff)}},
		{SourceID: "symbol:go:example.com/m:run", TargetID: "symbol:go:example.com/m:(Notifier).Notify",
			Kind: domain.FactCalls, Confidence: 1.0,
			Metadata: map[string]any{"line_num": 7.0, "pos": float64(notifyOff)}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:run", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || len(root.Nodes) != 2 {
		t.Fatalf("节点数 = %d; want 2（一行两个调用）:\n%+v", len(root.Nodes), root.Nodes)
	}
	n0, n1 := root.Nodes[0], root.Nodes[1]
	if n0.Label != "s.Save" || n1.Label != "n.Notify" {
		t.Fatalf("labels = %q/%q; want s.Save / n.Notify", n0.Label, n1.Label)
	}
	// 修复前（行号覆盖）：两个节点 Type 都是 m.Notifier（后写覆盖）
	if n0.Type != "m.Saver" {
		t.Errorf("第一个调用 Type = %q; want m.Saver（同行覆盖 bug——被 Notify 覆盖）", n0.Type)
	}
	if n1.Type != "m.Notifier" {
		t.Errorf("第二个调用 Type = %q; want m.Notifier", n1.Type)
	}
}

// TestSeqFilterLog（S5）：导出过滤——按函数名/包过滤的调用不生成节点。
func TestSeqFilterLog(t *testing.T) {
	src := `package m

import "example.com/m/log"

func run() {
	log.Printf("hi")
	svc()
}

func svc() {}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	logOff := strings.Index(src, "Printf(") + len("Printf")
	svcOff := strings.Index(src, "svc(") + len("svc")
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:run", Kind: domain.KindFunction, Name: "run", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:svc", Kind: domain.KindFunction, Name: "svc", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m/log:Printf", Kind: domain.KindFunction, Name: "Printf", FilePath: "log/log.go", LineStart: 1},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:run", TargetID: "symbol:go:example.com/m/log:Printf",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 7, "pos": float64(logOff)}},
		{SourceID: "symbol:go:example.com/m:run", TargetID: "symbol:go:example.com/m:svc",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 8, "pos": float64(svcOff)}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	// 按函数名过滤 log.Printf
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:run", RepoAbs: dir, Depth: 1,
		Filter: SeqFilter{Fns: []string{"Printf"}}})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil {
		t.Fatal("root nil")
	}
	if len(root.Nodes) != 1 || root.Nodes[0].Label != "svc" {
		t.Errorf("过滤后应只剩 svc 调用（log.Printf 被过滤）: %+v", root.Nodes)
	}
	// 按包过滤（log 包短名）
	root2, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:run", RepoAbs: dir, Depth: 1,
		Filter: SeqFilter{Pkgs: []string{"log"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(root2.Nodes) != 1 || root2.Nodes[0].Label != "svc" {
		t.Errorf("按包过滤后应只剩 svc: %+v", root2.Nodes)
	}
	// 无过滤：两个都在
	root3, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:run", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(root3.Nodes) != 2 {
		t.Errorf("无过滤应有两个调用: %+v", root3.Nodes)
	}
}
