//go:build integration

package integration

// todo30 B11：sequence-batch 导出全组合 Markdown 与时序图——真实源码 → init → CLI。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const u30B11Blockers = `package blockers
import("context";"database/sql")
var DB *sql.DB
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func Review(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if kind=="profile"{Complete(id)}
}
`

func TestU30B11SequenceBatch(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	root := t.TempDir()
	blockers := filepath.Join(root, "blockers")
	flow := filepath.Join(root, "flow")
	for _, d := range []string{blockers, flow} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(blockers, "go.mod"), "module example.com/blockers\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(blockers, "flow.go"), u30B11Blockers)
	writeFile(t, filepath.Join(flow, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(flow, "flow.go"), u30FlowSrc)
	for _, d := range []string{blockers, flow} {
		gitRun(t, d, "init")
		gitRun(t, d, "add", ".")
		gitRun(t, d, "commit", "-m", "fixture")
		clearIndex(t, d)
		if code := runCLI(t, "init", "--repo", d, "--workers", "1", "--progress", "none"); code != 0 {
			t.Fatalf("init %s exit = %d", d, code)
		}
	}
	manifest := `{"version":1,"flows":[{"id":"basic","repo":"./blockers","entry":"symbol:go:example.com/blockers:Submit","relatedEntries":["symbol:go:example.com/blockers:Review"]},{"id":"multi-review","repo":"./flow","entry":"symbol:go:example.com/flow:Submit","relatedEntries":["symbol:go:example.com/flow:FinishReview"]}],"formats":["markdown","svg"]}`
	mpath := filepath.Join(root, "flows.json")
	writeFile(t, mpath, manifest)
	outDir := filepath.Join(root, "exported")
	code, out := runCLIOut(t, "sequence-batch", "--manifest", mpath, "--out", outDir, "--json")
	if code != 0 {
		t.Fatalf("batch exit=%d\n%s", code, out)
	}
	var sum struct {
		Total    int      `json:"total"`
		Exported int      `json:"exported"`
		Failed   int      `json:"failed"`
		Files    []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		t.Fatalf("summary JSON: %v\n%s", err, out)
	}
	wantFiles := []string{"basic.md", "basic.svg", "multi-review.md", "multi-review.svg", "index.md"}
	if sum.Total != 2 || sum.Exported != 2 || sum.Failed != 0 ||
		len(sum.Files) != len(wantFiles) {
		t.Fatalf("summary = %+v（want 2/2/0 files=%v）", sum, wantFiles)
	}
	for i := range wantFiles {
		if sum.Files[i] != wantFiles[i] {
			t.Fatalf("files[%d] = %q（want %q）", i, sum.Files[i], wantFiles[i])
		}
		if _, err := os.Stat(filepath.Join(outDir, wantFiles[i])); err != nil {
			t.Fatalf("缺少产物 %s: %v", wantFiles[i], err)
		}
	}
	// 失败隔离：第三项不存在 → exported=2 failed=1，非零退出，成功文件保留。
	bad := `{"version":1,"flows":[{"id":"basic","repo":"./blockers","entry":"symbol:go:example.com/blockers:Submit","relatedEntries":["symbol:go:example.com/blockers:Review"]},{"id":"multi-review","repo":"./flow","entry":"symbol:go:example.com/flow:Submit","relatedEntries":["symbol:go:example.com/flow:FinishReview"]},{"id":"bad","repo":"./blockers","entry":"symbol:go:example.com/blockers:NoSuch"}],"formats":["markdown","svg"]}`
	bpath := filepath.Join(root, "flows-bad.json")
	writeFile(t, bpath, bad)
	outBad := filepath.Join(root, "exported-bad")
	code, out = runCLIOut(t, "sequence-batch", "--manifest", bpath, "--out", outBad, "--json")
	if code == 0 {
		t.Fatalf("含失败项应非零退出")
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		t.Fatalf("summary JSON: %v", err)
	}
	if sum.Total != 3 || sum.Exported != 2 || sum.Failed != 1 {
		t.Fatalf("失败摘要 = %+v（want 3/2/1）", sum)
	}
	for _, name := range []string{"basic.md", "multi-review.svg", "index.md"} {
		if _, err := os.Stat(filepath.Join(outBad, name)); err != nil {
			t.Fatalf("失败隔离丢失成功产物 %s", name)
		}
	}
}
