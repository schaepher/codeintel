//go:build integration

package integration

// todo27 T14：运行时输入兼容与严格来源边界（源码 → init → action 查询）。
//
// 覆盖：已解析 RuntimeData 与字符串路径一致；typed-nil 等同未配置；两个
// 路径入口不一致/对象与路径不一致 → 明确失败（前缀只出现一次）；同名局部
// *sql.DB / 已初始化变量不借绑定（两个分支保留）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const t14FlowSrc = `package flow
import("context";"database/sql")
var DB *sql.DB
func Hit(){}
func Miss(){}
func Config(ctx context.Context)(int,error){
 var enabled int
 err:=DB.QueryRowContext(ctx,"SELECT enabled FROM review_config WHERE region = ? AND node_type = ?","ID",2).Scan(&enabled)
 return enabled,err
}
func EntryBound(ctx context.Context){v,err:=Config(ctx);if err!=nil{return};if v==1{Hit()}else{Miss()}}
func EntryLocalShadow(ctx context.Context){
 var DB *sql.DB
 var v int
 _=DB.QueryRowContext(ctx,"SELECT enabled FROM review_config WHERE region = ? AND node_type = ?","ID",2).Scan(&v)
 if v==1{Hit()}else{Miss()}
}
var InitDB = (*sql.DB)(nil)
func EntryInitialized(ctx context.Context){
 var v int
 _=InitDB.QueryRowContext(ctx,"SELECT enabled FROM review_config WHERE region = ? AND node_type = ?","ID",2).Scan(&v)
 if v==1{Hit()}else{Miss()}
}
`

const t14RuntimeJSON = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/flow:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","node_type":2},"complete":true,"rows":[{"region":"ID","node_type":2,"enabled":1}]}]}]}`

func t14Fixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t14FlowSrc)
	rt := filepath.Join(dir, "runtime.json")
	writeFile(t, rt, t14RuntimeJSON)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir, rt
}

func t14Acts(t *testing.T, dir string) (*action.Actions, func()) {
	t.Helper()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return action.New(sqlite.NewRepo(db)), func() { db.Close() }
}

// t14Seq 查询并返回执行（确定）的符号计数。
func t14Seq(t *testing.T, acts *action.Actions, dir, entry string, req action.CodeSequenceRequest) map[string]int {
	t.Helper()
	req.Target = "symbol:go:example.com/flow:" + entry
	req.RepoAbs = dir
	req.Depth = 8
	root, err := acts.CodeSequence(req)
	if err != nil {
		t.Fatalf("CodeSequence(%s): %v", entry, err)
	}
	counts := map[string]int{}
	var walk func(n *action.CodeSeqNode)
	walk = func(n *action.CodeSeqNode) {
		if n == nil {
			return
		}
		if n.Evidence != nil && strings.HasPrefix(n.Evidence.SymbolID, "symbol:go:example.com/flow:") {
			counts[strings.TrimPrefix(n.Evidence.SymbolID, "symbol:go:example.com/flow:")]++
		}
		for _, c := range n.Nodes {
			walk(c)
		}
		for _, c := range n.Else {
			walk(c)
		}
	}
	walk(root)
	return counts
}

// TestT14ParsedVsPath：已解析快照与路径入口结论一致。
func TestT14ParsedVsPath(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, rt := t14Fixture(t)
	acts, closeFn := t14Acts(t, dir)
	defer closeFn()
	fromPath := t14Seq(t, acts, dir, "EntryBound", action.CodeSequenceRequest{RuntimeDataPath: rt})
	snap, err := action.LoadRuntimeSnapshot(rt)
	if err != nil {
		t.Fatal(err)
	}
	fromObj := t14Seq(t, acts, dir, "EntryBound", action.CodeSequenceRequest{Runtime: snap})
	if len(fromPath) == 0 || len(fromObj) == 0 {
		t.Fatalf("运行时分析应有执行证据：path=%v obj=%v", fromPath, fromObj)
	}
	for k, v := range fromPath {
		if fromObj[k] != v {
			t.Errorf("已解析快照与路径结论不一致：%s path=%d obj=%d", k, v, fromObj[k])
		}
	}
	if fromPath["Hit"] != 1 || fromPath["Miss"] != 0 {
		t.Errorf("快照 enabled=1 应执行 Hit（Hit=%d Miss=%d）", fromPath["Hit"], fromPath["Miss"])
	}
}

// TestT14TypedNilUnconfigured：typed-nil 等同未配置（保留原有 error 分支）。
func TestT14TypedNilUnconfigured(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, _ := t14Fixture(t)
	acts, closeFn := t14Acts(t, dir)
	defer closeFn()
	var nilSnap *action.RuntimeSnapshot
	base := t14Seq(t, acts, dir, "EntryBound", action.CodeSequenceRequest{})
	typedNil := t14Seq(t, acts, dir, "EntryBound", action.CodeSequenceRequest{Runtime: nilSnap})
	if base["Hit"] != typedNil["Hit"] || base["Miss"] != typedNil["Miss"] || base["Config"] != typedNil["Config"] {
		t.Errorf("typed-nil 应等同未配置：base=%v typedNil=%v", base, typedNil)
	}
	if base["Hit"]+base["Miss"] != 2 || base["Config"] != 1 {
		t.Errorf("无运行时时应保留原两分支与读取调用：%v", base)
	}
}

// TestT14InconsistentInputs：路径入口互相不一致 / 对象与路径不一致 → 失败。
func TestT14InconsistentInputs(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, rt := t14Fixture(t)
	acts, closeFn := t14Acts(t, dir)
	defer closeFn()
	other := filepath.Join(dir, "runtime2.json")
	if err := os.WriteFile(other, []byte(t14RuntimeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := acts.CodeSequence(action.CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:EntryBound", RepoAbs: dir, Depth: 4,
		RuntimeDataPath: rt, RuntimeData: other})
	if err == nil {
		t.Fatalf("两个路径入口不一致应失败")
	}
	if got := strings.Count(err.Error(), "runtime-data:"); got != 1 {
		t.Errorf("错误前缀应只出现一次（got %d）：%v", got, err)
	}
	wrong := &action.RuntimeSnapshot{Hash: "deadbeef", Version: 1}
	if _, err := acts.CodeSequence(action.CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:EntryBound", RepoAbs: dir, Depth: 4,
		RuntimeDataPath: rt, Runtime: wrong}); err == nil {
		t.Errorf("已解析快照与路径不一致应失败")
	}
}

// TestT14DBIdentityStrict：同名局部 / 已初始化变量不借绑定。
func TestT14DBIdentityStrict(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, rt := t14Fixture(t)
	acts, closeFn := t14Acts(t, dir)
	defer closeFn()
	for _, entry := range []string{"EntryLocalShadow", "EntryInitialized"} {
		c := t14Seq(t, acts, dir, entry, action.CodeSequenceRequest{RuntimeDataPath: rt})
		if c["Hit"] != 1 || c["Miss"] != 1 {
			t.Errorf("%s：不借绑定应保留两个分支（Hit=%d Miss=%d）", entry, c["Hit"], c["Miss"])
		}
	}
}
