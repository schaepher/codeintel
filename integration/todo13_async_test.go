//go:build integration

package integration

// todo13-5 A3：生产索引大反例——seed 无字段摘要时不得退回全包候选。
//   - 2000 个同包 Unrelated 类型：不得进入节点/关系
//   - 真实 ScheduleOnly → Read 关联保留；候选总数不是全包函数数
//   - Entry 字段身份对照仍为 2 节点/1 关系；输出体积对照原 ~6.7MB

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func TestTodo13AsyncNoSummaryLargeRealIndex(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/codeintel-large\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("package large\n\n")
	b.WriteString("type Job struct{ RecordID string }\n\n")
	b.WriteString("func Entry(j *Job, id string) { j.RecordID = id }\n")
	b.WriteString("func Read(j *Job) { _ = j.RecordID }\n")
	b.WriteString("func ScheduleOnly(j *Job) { Read(j) }\n\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "type Unrelated%d struct{ Cache string }\n", i)
		fmt.Fprintf(&b, "func (u *Unrelated%d) Store() { u.Cache = \"x\"; u.Read() }\n", i)
		fmt.Fprintf(&b, "func (u *Unrelated%d) Read() string { return u.Cache }\n\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	base := "symbol:go:example.com/codeintel-large:"
	code, out := runCLIOut(t, "query", "async", base+"ScheduleOnly", "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("async exit = %d; %s", code, out)
	}
	var res action.AsyncLifecycleResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("async JSON: %v", err)
	}
	t.Logf("输出字节数=%d（原反例约 6.7MB）；candidate_total=%d nodes=%d links=%d basis=%s",
		len(out), res.CandidateTotal, len(res.Nodes), len(res.Links), res.CandidateBasis)
	if len(out) > 1<<20 {
		t.Errorf("输出体积 %d 字节，仍接近原全包候选规模", len(out))
	}
	for _, n := range res.Nodes {
		if strings.Contains(n.ID, "Unrelated") {
			t.Errorf("Unrelated* 不得进入节点: %s", n.ID)
		}
	}
	if res.CandidateTotal > 10 {
		t.Errorf("候选总数 %d 不应接近全包函数数", res.CandidateTotal)
	}
	hasRead := false
	for _, n := range res.Nodes {
		if n.ID == base+"Read" {
			hasRead = true
		}
	}
	if !hasRead {
		t.Errorf("真实调用邻居 Read 必须保留: %+v", res.Nodes)
	}
	for _, l := range res.Links {
		if strings.Contains(l.From, "Unrelated") || strings.Contains(l.To, "Unrelated") {
			t.Errorf("Unrelated* 关系数应为 0: %+v", l)
		}
	}
	if res.CandidateBasis != "call_identity" {
		t.Errorf("候选依据应为调用身份: %+v", res)
	}

	// Entry 字段身份对照
	code, out = runCLIOut(t, "query", "async", base+"Entry", "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("Entry async exit = %d; %s", code, out)
	}
	res = action.AsyncLifecycleResult{}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 2 || len(res.Links) != 1 {
		t.Errorf("Entry 对照应为 2 节点/1 关系: nodes=%d links=%d", len(res.Nodes), len(res.Links))
	}
	for _, n := range res.Nodes {
		if strings.Contains(n.ID, "Unrelated") {
			t.Errorf("Entry 对照不得含无关节点: %s", n.ID)
		}
	}
}
