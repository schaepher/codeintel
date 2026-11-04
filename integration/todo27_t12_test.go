//go:build integration

package integration

// todo27 T12：在原调用上下文同步续查（真实 init → CLI）。
//
// 覆盖：depth=1 + --continue-segments 的前沿**上下文快照**——同函数不同实参/
// 回调各自绑定、调用后覆写不污染前沿、工厂字段续查到与完整展开相同的身份
// （候选保持未消歧，不升级为确定执行）。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t12Src = `package flow
func Hit(){}
func Miss(){}
func OneEvent(){}
func TwoEvent(){}
func Execute(f func()){f()}
func A(){Hit()}
func B(){Miss()}
type API interface{Work()}
type One struct{}
type Two struct{}
func(*One)Work(){OneEvent()}
func(*Two)Work(){TwoEvent()}
type Box struct{worker API}
func(b *Box)Set(v API){b.worker=v}
func(b *Box)Get()API{return b.worker}
func(b *Box)Run(){b.Step()}
func(b *Box)Step(){b.Get().Work()}
func NewOne()*One{return &One{}}
func NewTwo()*Two{return &Two{}}
func Build(kind int)(*Box,error){
 box:=&Box{}
 switch kind{case 1:box.Set(NewOne());case 2:box.Set(NewTwo());default:return nil,nil}
 return box,nil
}
func EntryArgs(){Execute(A);Execute(B)}
func EntryOverwrite(){f:=A;Execute(f);f=B}
func EntryFactory(){box,err:=Build(1);if err!=nil{return};box.Run()}
`

func t12Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t12Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func t12Seq(t *testing.T, dir, entry string, depth, segs int) string {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/flow:" + entry,
		"--repo", dir, "--code", "--depth", itoaInt(depth), "--format", "mermaid"}
	if segs > 0 {
		args = append(args, "--continue-segments", itoaInt(segs))
	}
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	return out
}

// TestT12FrontierArgSnapshot：不同实参/回调的前沿各自保留绑定。
func TestT12FrontierArgSnapshot(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t12Fixture(t)
	out := t12Seq(t, dir, "EntryArgs", 1, 4)
	if strings.Count(out, "id=symbol:go:example.com/flow:Hit verdict=") != 1 ||
		strings.Count(out, "id=symbol:go:example.com/flow:Miss verdict=") != 1 {
		t.Errorf("两个前沿应各自保留回调绑定（Hit=1/Miss=1）\n%s", out)
	}
}

// TestT12FrontierNotPolluted：调用后的覆写不污染已保存的前沿环境。
func TestT12FrontierNotPolluted(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t12Fixture(t)
	out := t12Seq(t, dir, "EntryOverwrite", 1, 4)
	if !strings.Contains(out, "flow:Hit verdict=") {
		t.Errorf("前沿应保留调用时绑定（Hit）\n%s", out)
	}
	if strings.Contains(out, "flow:Miss verdict=") {
		t.Errorf("调用后的覆写不得污染前沿（Miss 不应执行）\n%s", out)
	}
}

// TestT12FactoryFieldContext：工厂字段前沿的续查身份与完整展开一致
// （候选未消歧，不升级为确定执行）。
func TestT12FactoryFieldContext(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t12Fixture(t)
	depth1 := t12Seq(t, dir, "EntryFactory", 1, 16)
	full := t12Seq(t, dir, "EntryFactory", 12, 0)
	for _, want := range []string{"One.Work", "Two.Work"} {
		if !strings.Contains(depth1, want) {
			t.Errorf("depth1+续查应保留工厂候选 %s\n%s", want, depth1)
		}
		if !strings.Contains(full, want) {
			t.Errorf("完整展开应保留工厂候选 %s\n%s", want, full)
		}
	}
	if strings.Contains(depth1, "无法证明") || strings.Contains(depth1, "unknown_receiver") {
		t.Errorf("续查不应退化成 unknown\n%s", depth1)
	}
	if !strings.Contains(depth1, "iface_ambiguous") && !strings.Contains(depth1, "候选") {
		t.Errorf("候选应保持未消歧诊断\n%s", depth1)
	}
}
