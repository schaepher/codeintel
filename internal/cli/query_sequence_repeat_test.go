package cli

// todo20-9：重复调用同一本地函数——CLI JSON 必须保留两个调用位置与
// 目标身份，depth=1 两处都因 depth 停止，depth=2 两处都展开返回。
// （索引 calls 边按函数对汇总，只有首个位置带 metadata。）

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const seqRepeatCLISrc = `package m

func Submit(region string) string {
	if region == "CN" {
		return Save()
	}
	return Save()
}

func Save() string { return "pending" }
`

func seedSeqRepeatCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(seqRepeatCLISrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Submit", Kind: domain.KindFunction, Name: "Submit",
			FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:Save", Kind: domain.KindFunction, Name: "Save",
			FilePath: "main.go", LineStart: 10},
	}
	facts := []*domain.Fact{{
		SourceID: "symbol:go:example.com/m:Submit", TargetID: "symbol:go:example.com/m:Save",
		Kind: domain.FactCalls, Confidence: 0.8,
		Metadata: map[string]any{"line_num": float64(5)},
	}}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "seqrepeatcli", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// seqJSONCalls 收集 JSON 树里指定 Label 的调用节点（含 Else）。
func seqJSONCalls(n map[string]any, label string, out *[]map[string]any) {
	if n == nil {
		return
	}
	if n["Label"] == label && n["Kind"] == "call" {
		*out = append(*out, n)
	}
	for _, key := range []string{"Nodes", "Else"} {
		if cs, ok := n[key].([]any); ok {
			for _, c := range cs {
				if m, ok := c.(map[string]any); ok {
					seqJSONCalls(m, label, out)
				}
			}
		}
	}
}

func TestCodeSequenceRepeatCLIJSON(t *testing.T) {
	dir := seedSeqRepeatCLI(t)
	const saveID = "symbol:go:example.com/m:Save"
	for _, depth := range []string{"1", "2"} {
		out := captureStdout(func() {
			if code := cmdQuery([]string{"sequence", "Submit", "--repo", dir, "--code",
				"--depth", depth, "--evidence", "--json"}); code != 0 {
				t.Fatalf("depth=%s exit=%d", depth, code)
			}
		})
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("depth=%s JSON: %v\n%s", depth, err, out)
		}
		var calls []map[string]any
		seqJSONCalls(root, "Save", &calls)
		if len(calls) != 2 {
			t.Fatalf("depth=%s 应保留两个 Save 调用点，实为 %d\n%s", depth, len(calls), out)
		}
		wantLines := []int{5, 7}
		for i, c := range calls {
			if int(c["Line"].(float64)) != wantLines[i] {
				t.Errorf("depth=%s 第 %d 个 Save 行号=%v，期望 %d", depth, i, c["Line"], wantLines[i])
			}
			ev, _ := c["evidence"].(map[string]any)
			if ev == nil || ev["symbol_id"] != saveID {
				t.Errorf("depth=%s 行 %v 目标身份缺失: %+v", depth, c["Line"], ev)
				continue
			}
			if ev["verdict"] == "unverified" {
				t.Errorf("depth=%s 行 %v 已解析却标 unverified: %+v", depth, c["Line"], ev)
			}
			stop, _ := c["stop"].(map[string]any)
			if depth == "1" {
				if stop == nil || stop["code"] != "depth" {
					t.Errorf("depth=1 行 %v 应因 depth 停止，实为 %+v", c["Line"], stop)
				}
				continue
			}
			if stop != nil && stop["code"] == "unresolved" {
				t.Errorf("depth=2 行 %v 不应 unresolved", c["Line"])
			}
			children, _ := c["Nodes"].([]any)
			if len(children) != 1 {
				t.Fatalf("depth=2 行 %v 应展开一个 return 子节点，实为 %d", c["Line"], len(children))
			}
			ret, _ := children[0].(map[string]any)
			if ret == nil || ret["Kind"] != "return" ||
				ret["Label"] != `return "pending"` {
				t.Errorf("depth=2 行 %v 展开内容不对: %+v", c["Line"], ret)
			}
		}
		meta, _ := root["meta"].(map[string]any)
		diags, _ := meta["diagnostics"].([]any)
		unresolved := 0
		depthDiag := 0
		for _, d := range diags {
			dm, _ := d.(map[string]any)
			switch dm["code"] {
			case "unresolved":
				unresolved++
			case "depth":
				depthDiag = int(dm["count"].(float64))
			}
		}
		if unresolved != 0 {
			t.Errorf("depth=%s 不应有 unresolved 诊断: %+v", depth, diags)
		}
		if depth == "1" && depthDiag != 2 {
			t.Errorf("depth=1 应 depth×2，实为 %d: %+v", depthDiag, diags)
		}
	}
}
