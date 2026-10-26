package action

// todo12-12：多个具名返回值保留独立槽身份。
//   - 同组多名 `(state, reason string)` 必须展开成两个槽（曾把
//     f.Names[0] 复制两次 → ["state","state"]）
//   - 裸 return（具名结果）没有返回表达式，槽身份仍按声明顺序展开

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestCodeSequenceGroupedResultSlots(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

func Named() (state, reason string) {
	return "approved", "checked"
}

func Bare() (state, reason string) {
	state = "ok"
	reason = "why"
	return
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Named", Kind: domain.KindFunction, Name: "Named", FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:Bare", Kind: domain.KindFunction, Name: "Bare", FilePath: "main.go", LineStart: 7},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(r)

	named, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:Named", RepoAbs: dir, Depth: 1})
	if err != nil || named == nil {
		t.Fatalf("Named CodeSequence: %v root=%v", err, named)
	}
	ret := findReturnNode(named)
	if ret == nil {
		t.Fatalf("Named 应输出 return 节点: %+v", named.Nodes)
	}
	if len(ret.Slots) != 2 || ret.Slots[0] != "state" || ret.Slots[1] != "reason" {
		t.Errorf("同组多名结果槽应为 [state reason]，实为 %+v", ret.Slots)
	}
	if len(ret.Values) != 2 || ret.Values[0] != `"approved"` || ret.Values[1] != `"checked"` {
		t.Errorf("返回值位置不应改变: %+v", ret.Values)
	}

	bare, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:Bare", RepoAbs: dir, Depth: 1})
	if err != nil || bare == nil {
		t.Fatalf("Bare CodeSequence: %v root=%v", err, bare)
	}
	bret := findReturnNode(bare)
	if bret == nil {
		t.Fatalf("裸 return 应输出 return 节点: %+v", bare.Nodes)
	}
	if len(bret.Values) != 0 {
		t.Errorf("裸 return 没有返回表达式: %+v", bret.Values)
	}
	if len(bret.Slots) != 2 || bret.Slots[0] != "state" || bret.Slots[1] != "reason" {
		t.Errorf("裸 return 的槽应按声明顺序展开，实为 %+v", bret.Slots)
	}
	if !bret.Terminates {
		t.Errorf("return 节点应终止当前路径: %+v", bret)
	}
}

// findReturnNode 树里第一个 return 节点。
func findReturnNode(n *CodeSeqNode) *CodeSeqNode {
	if n == nil {
		return nil
	}
	if n.Kind == "return" {
		return n
	}
	if got := findReturnNodeIn(n.Nodes); got != nil {
		return got
	}
	return findReturnNodeIn(n.Else)
}

func findReturnNodeIn(nodes []*CodeSeqNode) *CodeSeqNode {
	for _, n := range nodes {
		if got := findReturnNode(n); got != nil {
			return got
		}
	}
	return nil
}
