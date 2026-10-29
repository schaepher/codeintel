//go:build integration

package integration

// todo27 T04：未知返回 / 混合写者 / 局部遮蔽 / 条件 switch 的实例来源边界
// （真实 init → CLI）。
//
// 规则：任一未知 return（nil/未解析调用/形参）使"唯一具体来源"失效；
// 具体 + 未知混写不能维持唯一实现；局部遮蔽不借包级单例；switch 守卫在
// 调用点实参已知时裁剪；不同对象的 getter 字段证明不串用（请求内缓存）。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t04PositiveSrc = `package flow
type API interface{Work()}
type One struct{}
func(*One)Work(){Hit()}
func Hit(){}
var current API
func Inject(v API){current=v}
func init(){Inject(&One{})}
func Get()API{return current}
func Entry(){Get().Work()}
`

const t04VariantSrc = `package flow
type API interface{Work()}
type One struct{}
type Two struct{}
func(*One)Work(){HitOne()}
func(*Two)Work(){HitTwo()}
func HitOne(){}
func HitTwo(){}
var current API
func Inject(v API){current=v}
func init(){Inject(&One{})}
func Get()API{return current}
func Entry(){Get().Work()}
func GetNil(k bool)API{if k{return &One{}};return nil}
func EntryNilParam(k bool){GetNil(k).Work()}
func pickUnknown()API{return nil}
func GetMix(k bool)API{if k{return &One{}};return pickUnknown()}
func EntryMixParam(k bool){GetMix(k).Work()}
func GetParam(v API)API{return v}
func EntryParam(){GetParam(&One{}).Work()}
func LocalShadow(){current:=API(&Two{});current.Work()}
func FromField(kind int)API{switch kind{case 1:return &One{};case 2:return &Two{}};return nil}
func EntrySwitch(kind int){FromField(kind).Work()}
func EntrySwitchLit(){FromField(1).Work()}
type BoxA struct{biz API}
func NewBoxA()*BoxA{return &BoxA{biz:&One{}}}
func(b *BoxA)Get()API{return b.biz}
type BoxB struct{biz API}
func NewBoxB()*BoxB{return &BoxB{biz:&Two{}}}
func(b *BoxB)Get()API{return b.biz}
func EntryTwoGetters(){NewBoxA().Get().Work();NewBoxB().Get().Work()}
`

func t04Init(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// t04Seq 查询并返回 (mermaid, json root)。
func t04Seq(t *testing.T, dir, entry string) (string, map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	return out, querySeqJSON(t, dir, "symbol:go:example.com/flow:"+entry, "--depth", "12")
}

// TestT04UnknownReturnsInvalidate：nil / 未解析调用 / 形参返回使具体来源失效。
func TestT04UnknownReturnsInvalidate(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t04Init(t, t04VariantSrc)
	for _, entry := range []string{"EntryNilParam", "EntryMixParam", "EntryParam"} {
		out, _ := t04Seq(t, dir, entry)
		if !strings.Contains(out, "unknown_receiver") {
			t.Errorf("%s：未知 return 应输出 unknown_receiver\n%s", entry, out)
		}
		if plain, amb := t02Hits(t, dir, entry, "HitOne"); plain != 0 || amb != 0 {
			t.Errorf("%s：未知 return 不应确定/候选执行 HitOne（plain=%d amb=%d）", entry, plain, amb)
		}
	}
}

// TestT04MixedWriterAndShadow：混合写者失效 + 局部遮蔽不借包级单例。
func TestT04MixedWriterAndShadow(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t04Init(t, t04VariantSrc)
	out, _ := t04Seq(t, dir, "Entry")
	if !strings.Contains(out, "unknown_receiver") {
		t.Errorf("混合写者（init + 运行时注入）应 unknown_receiver\n%s", out)
	}
	// 局部遮蔽：`current := API(&Two{})` —— 具体 Two，不借包级单例的 One。
	if plain, _ := t02Hits(t, dir, "LocalShadow", "HitTwo"); plain != 1 {
		t.Errorf("LocalShadow 应执行 HitTwo 一次，got %d", plain)
	}
	if plain, amb := t02Hits(t, dir, "LocalShadow", "HitOne"); plain+amb != 0 {
		t.Errorf("LocalShadow 不应执行包级单例的 HitOne（plain=%d amb=%d）", plain, amb)
	}
}

// TestT04SwitchGuardsAndCacheIsolation：switch 守卫裁剪 + 不同对象 getter
// 字段证明不串用。
func TestT04SwitchGuardsAndCacheIsolation(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t04Init(t, t04VariantSrc)
	// 已知实参 kind=1 → 只执行 case 1 的 One。
	if plain, _ := t02Hits(t, dir, "EntrySwitchLit", "HitOne"); plain != 1 {
		t.Errorf("EntrySwitchLit 应执行 HitOne 一次，got %d", plain)
	}
	if plain, amb := t02Hits(t, dir, "EntrySwitchLit", "HitTwo"); plain+amb != 0 {
		t.Errorf("EntrySwitchLit 不应执行 case 2（plain=%d amb=%d）", plain, amb)
	}
	// 未知实参 → 保留守卫边界（unknown），不挑实现。
	out, _ := t04Seq(t, dir, "EntrySwitch")
	if !strings.Contains(out, "unknown_receiver") {
		t.Errorf("未知 kind 应 unknown_receiver\n%s", out)
	}
	// 同一请求内两个不同对象的 getter 字段证明不串用。
	if plain, _ := t02Hits(t, dir, "EntryTwoGetters", "HitOne"); plain != 1 {
		t.Errorf("EntryTwoGetters 的 BoxA 应执行 HitOne 一次，got %d", plain)
	}
	if plain, _ := t02Hits(t, dir, "EntryTwoGetters", "HitTwo"); plain != 1 {
		t.Errorf("EntryTwoGetters 的 BoxB 应执行 HitTwo 一次，got %d", plain)
	}
}

// TestT04SingletonPositive：init 唯一写者（无未知调用者）仍确定执行。
func TestT04SingletonPositive(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t04Init(t, t04PositiveSrc)
	out, _ := t04Seq(t, dir, "Entry")
	if !strings.Contains(out, "id=symbol:go:example.com/flow:Hit verdict=") {
		t.Errorf("init 唯一写者的单例应确定执行 Hit\n%s", out)
	}
	if strings.Contains(out, "unknown_receiver") {
		t.Errorf("init 唯一写者不应 unknown_receiver\n%s", out)
	}
}
