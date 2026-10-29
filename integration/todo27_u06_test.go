//go:build integration

package integration

// todo27 U06：未知实例来源的剩余入口（getter / 混合写者 / 形参别名 /
// 包级单例未知注入 / 接口根入口 / depth=1 续查）。
//
// 生产链路：源码 → init → CLI（不手灌图）。正例（构造器 / 局部具体实例）
// 仍必须 Hit=1；未知来源只保留 unknown_receiver 或未验证候选子树。

import (
	"path/filepath"
	"strings"
	"testing"
)

// u06Fixture 写入 E-U06 夹具源码（含正例 Known/KnownLocal）。
func u06Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/receiver\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), `package receiver
type API interface{ Work() }
type One struct{}
func(*One)Work(){Hit()}
func Hit(){}
type Empty struct{biz API}
func(s *Empty)Entry(){s.biz.Work()}
type Getter struct{biz API}
func(s *Getter)Get()API{return s.biz}
func(s *Getter)Entry(){s.Get().Work()}
var current API
func Inject(v API){current=v}
func Get()API{return current}
func init(){Inject(&One{})}
func UnknownInject(v API){Inject(v)}
func Singleton(){Get().Work()}
func Parameter(v API){v.Work()}
func Local(v API){x:=v;x.Work()}
type Mixed struct{biz API}
func NewMixed()*Mixed{return &Mixed{biz:&One{}}}
func(s *Mixed)Set(v API){s.biz=v}
func(s *Mixed)Entry(){s.biz.Work()}
type Known struct{biz API}
func NewKnown()*Known{return &Known{biz:&One{}}}
func(s *Known)Entry(){s.biz.Work()}
func KnownEntry(){NewKnown().Entry()}
func KnownLocal(){var v API=&One{};v.Work()}
`)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u06Seq 跑一次代码级时序查询（mermaid）。
func u06Seq(t *testing.T, dir, sym string, depth, segments int) string {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/receiver:"+sym,
		"--repo", dir, "--code", "--depth", itoaInt(depth),
		"--continue-segments", itoaInt(segments), "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query sequence %s exit=%d\n%s", sym, code, out)
	}
	return out
}

func itoaInt(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// assertU06Unknown 断言：未知实例来源的诊断存在，且没有确定执行 Hit。
func assertU06Unknown(t *testing.T, label, out string) {
	t.Helper()
	if !strings.Contains(out, "unknown_receiver") {
		t.Errorf("%s：应输出 unknown_receiver 诊断\n%s", label, out)
	}
	if strings.Contains(out, "id=symbol:go:example.com/receiver:Hit verdict=") &&
		!strings.Contains(out, "candidate") {
		// 候选子树的 Hit 允许出现，但必须伴随候选标记（iface 入口）。
		if !strings.Contains(out, "unverified") {
			t.Errorf("%s：未知来源不应确定执行 Hit\n%s", label, out)
		}
	}
}

// TestU06UnknownInstanceSources：getter / 混合写者 / 形参 / 别名 /
// 包级单例未知注入 / 接口根入口——depth 12 与 depth 1+续查同口径。
func TestU06UnknownInstanceSources(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u06Fixture(t)
	cases := []struct{ sym, label string }{
		{"(Getter).Entry", "getter 方法返回字段"},
		{"(Mixed).Entry", "混合写者（构造器具体 + setter 形参）"},
		{"Parameter", "接口形参"},
		{"Local", "形参别名"},
		{"Singleton", "包级单例（未知 Inject 调用者）"},
	}
	for _, c := range cases {
		for _, depth := range []int{12, 1} {
			out := u06Seq(t, dir, c.sym, depth, 32)
			assertU06Unknown(t, c.label+" depth="+itoaInt(depth), out)
		}
	}
}

// TestU06IfaceRootEntry：接口方法作为查询入口（无方法体）——实现只能是
// 未验证候选（候选后代不计确定执行、不参与续查）。
func TestU06IfaceRootEntry(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u06Fixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/receiver:(API).Work",
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32",
		"--format", "mermaid", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	for _, want := range []string{"iface_ambiguous", "interface-entry", "\"candidate\": true"} {
		if !strings.Contains(out, want) {
			t.Errorf("接口根入口应包含 %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, "unverified") {
		t.Errorf("候选子树必须标 candidate=true\n%s", out)
	}
}

// TestU06KnownInstancePositive：构造器 / 局部具体实例仍确定执行（Hit=1）。
func TestU06KnownInstancePositive(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u06Fixture(t)
	for _, sym := range []string{"KnownEntry", "KnownLocal"} {
		out := u06Seq(t, dir, sym, 12, 32)
		if !strings.Contains(out, "id=symbol:go:example.com/receiver:Hit verdict=") {
			t.Errorf("%s：准确实例来源应确定执行 Hit\n%s", sym, out)
		}
		if strings.Contains(out, "unknown_receiver") {
			t.Errorf("%s：准确实例来源不应标 unknown_receiver\n%s", sym, out)
		}
	}
}
