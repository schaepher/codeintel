//go:build integration

package integration

// todo27 T05：返回实例跨方法/闭包/拷贝的来源一致性（真实 init → CLI）。
//
// 覆盖：调用前覆写取最后一次写入、重复 setter 取最后一次、不同对象各自
// 来源、无 default 的 switch 不沿用旧字段、取址逃逸与未知方法调用使证明
// 失效；工厂路径（E-T05 正例）仍以 kind 守卫给出 One/Two 候选。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t05Src = `package flow
type API interface{Work()}
type One struct{}
type Two struct{}
func(*One)Work(){HitOne()}
func(*Two)Work(){HitTwo()}
func HitOne(){}
func HitTwo(){}
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
func Entry(){box,err:=Build(1);if err!=nil{return};box.Run()}
func BuildNoDefault(kind int)*Box{box:=&Box{};switch kind{case 1:box.Set(NewOne());case 2:box.Set(NewTwo())};return box}
func EntryNoDefault(){BuildNoDefault(3).Run()}
func EntryRepeat(){box:=&Box{};box.Set(NewOne());box.Set(NewTwo());box.Run()}
func EntryOverwrite(){box:=&Box{};box.Set(NewTwo());box.Set(NewOne());box.Run()}
func EntryOther(){a:=&Box{};b:=&Box{};a.Set(NewOne());b.Set(NewTwo());a.Run();b.Run()}
func EntryAliasEscape(){box:=&Box{};box.Set(NewOne());p:=&box;*p=&Box{};box.Run()}
func EntryUnknownMethod(){box:=&Box{};box.Set(NewOne());box.Unknown();box.Run()}
func(b *Box)Unknown(){}
`

func t05Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t05Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func t05Seq(t *testing.T, dir, entry string) string {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	return out
}

// TestT05LocalReachingDefs：局部实例的写入顺序（覆写/重复 setter/多对象）。
func TestT05LocalReachingDefs(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t05Fixture(t)
	// 调用前覆写：最后一次 Set(NewOne()) 生效。
	if p, _ := t02Hits(t, dir, "EntryOverwrite", "HitOne"); p != 1 {
		t.Errorf("EntryOverwrite 应执行 HitOne 一次，got %d", p)
	}
	if p, a := t02Hits(t, dir, "EntryOverwrite", "HitTwo"); p+a != 0 {
		t.Errorf("EntryOverwrite 不应执行旧的 HitTwo（plain=%d amb=%d）", p, a)
	}
	// 重复 setter：最后一次 Set(NewTwo()) 生效。
	if p, _ := t02Hits(t, dir, "EntryRepeat", "HitTwo"); p != 1 {
		t.Errorf("EntryRepeat 应执行 HitTwo 一次，got %d", p)
	}
	if p, a := t02Hits(t, dir, "EntryRepeat", "HitOne"); p+a != 0 {
		t.Errorf("EntryRepeat 不应执行被覆盖的 HitOne（plain=%d amb=%d）", p, a)
	}
	// 不同对象：a→One，b→Two（不串用）。
	if p, _ := t02Hits(t, dir, "EntryOther", "HitOne"); p != 1 {
		t.Errorf("EntryOther 的 a 应执行 HitOne 一次，got %d", p)
	}
	if p, _ := t02Hits(t, dir, "EntryOther", "HitTwo"); p != 1 {
		t.Errorf("EntryOther 的 b 应执行 HitTwo 一次，got %d", p)
	}
}

// TestT05Invalidation：取址逃逸 / 未知方法 / 无 default switch 不沿用旧值。
func TestT05Invalidation(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t05Fixture(t)
	for _, entry := range []string{"EntryAliasEscape", "EntryUnknownMethod"} {
		out := t05Seq(t, dir, entry)
		if !strings.Contains(out, "unknown_receiver") {
			t.Errorf("%s 应 unknown_receiver\n%s", entry, out)
		}
		if p, a := t02Hits(t, dir, entry, "HitOne"); p+a != 0 {
			t.Errorf("%s 不应执行旧字段的 HitOne（plain=%d amb=%d）", entry, p, a)
		}
	}
	if out := t05Seq(t, dir, "EntryNoDefault"); !strings.Contains(out, "unknown_receiver") {
		t.Errorf("无 default 的 switch 不应沿用旧字段\n%s", out)
	}
}

// TestT05FactoryGuardBaseline：E-T05 正例（工厂 setter + kind 守卫）保持——
// 两个候选各自带守卫，One/Two 都能执行（互斥分支，不拼成顺序）。
func TestT05FactoryGuardBaseline(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t05Fixture(t)
	out := t05Seq(t, dir, "Entry")
	for _, want := range []string{"factory-field-dataflow", "alt kind == 1", "alt kind == 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("E-T05 正例应包含 %q\n%s", want, out)
		}
	}
}
