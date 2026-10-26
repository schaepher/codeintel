//go:build integration

package integration

// todo13-4 A2：真实索引 → CLI（Mermaid）保留接口实现的完整候选。
//   - 无场景：参与者“实现未消歧”，图内列出两个候选，不展开单一实现
//   - JSON/文本/图使用同一候选身份

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const todo13ImplSrc = `package regression

type Doer interface{ Do() }

type ImplCN struct{}

func (ImplCN) Do() {}

type ImplUS struct{}

func (ImplUS) Do() {}

func Pick(platform string) {
	var d Doer
	if platform == "CN" {
		d = &ImplCN{}
	} else {
		d = &ImplUS{}
	}
	d.Do()
}
`

func TestTodo13ImplCandidatesRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-regression\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pick.go"), []byte(todo13ImplSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	sym := "symbol:go:example.com/codeintel-regression:Pick"

	code, out := runCLIOut(t, "query", "sequence", sym, "--repo", dir,
		"--code", "--depth", "3", "--evidence", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid exit = %d; %s", code, out)
	}
	for _, want := range []string{"实现未消歧", "ImplCN.Do", "ImplUS.Do", "unverified"} {
		if !strings.Contains(out, want) {
			t.Errorf("mermaid 应含 %q:\n%s", want, out)
		}
	}
	// JSON 候选与图内候选身份一致
	root := querySeqJSON(t, dir, sym, "--depth", "3")
	var call map[string]any
	walkSeqJSON(root, func(n map[string]any) {
		if lbl, _ := n["Label"].(string); lbl == "d.Do" {
			call = n
		}
	})
	if call == nil {
		t.Fatalf("未找到 d.Do: %+v", root)
	}
	ev, _ := call["evidence"].(map[string]any)
	if ev == nil || ev["ambiguous"] != true {
		t.Fatalf("JSON 应 ambiguous: %+v", call)
	}
	cands, _ := ev["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("JSON 应保留两项候选: %+v", ev)
	}
	for _, c := range cands {
		s, _ := c.(string)
		short := seqCandidateFixtureShort(s)
		if !strings.Contains(out, short) {
			t.Errorf("图内候选应与 JSON 一致（缺 %s）:\n%s", short, out)
		}
	}
	// 文本同口径
	code, out = runCLIOut(t, "query", "sequence", sym, "--repo", dir,
		"--code", "--depth", "3", "--evidence")
	if code != 0 {
		t.Fatalf("text exit = %d; %s", code, out)
	}
	if !strings.Contains(out, "候选未消歧") || !strings.Contains(out, "ImplCN.Do") || !strings.Contains(out, "ImplUS.Do") {
		t.Errorf("文本应列出两项候选:\n%s", out)
	}
}

// seqCandidateFixtureShort canonical 候选 ID → (ImplCN).Do 形态短名。
func seqCandidateFixtureShort(id string) string {
	s := id
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(s, "(")
	return strings.Replace(s, ").", ".", 1)
}
