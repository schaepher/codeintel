//go:build integration

package integration

// todo27 T07：先解析调用点身份，再应用 depth（真实 init → CLI）。
//
// 覆盖：局部变量遮蔽导入别名（不得借包函数）、同一行同名跨包调用身份、
// depth=1 与完整展开身份一致、单实现但未知实例不产生确定执行。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t07FlowSrc = `package flow
import other "example.com/flow/other"
type S struct{}
func(s *S)Run(){HitS()}
func HitS(){}
func HitPkg(){}
func HitPkg2(){}
func HitLocal(){}
func HitA(){}
func HitB(){}
func A(){HitA()}
func B(){HitB()}
func Do(){HitLocal()}
func EntryShadow(){other:=&S{};other.Run()}
func EntryCross(){other.Do();Do()}
type API interface{Work()}
type One struct{}
func(*One)Work(){HitA()}
func EntryUnknown(v API){v.Work()}
`

const t07OtherSrc = `package other
func Run(){HitPkg()}
func HitPkg(){}
func Do(){HitPkg2()}
func HitPkg2(){}
`

func t07Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t07FlowSrc)
	writeFile(t, filepath.Join(dir, "other", "other.go"), t07OtherSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// t07CallIDs 返回 (行内 evidence 的 symbol ID 列表)，按出现顺序。
func t07CallIDs(t *testing.T, dir, entry string, depth int) []string {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:"+entry,
		"--repo", dir, "--code", "--depth", itoaInt(depth), "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "%% evidence P0->") {
			continue
		}
		i := strings.Index(line, " id=")
		if i < 0 {
			continue
		}
		rest := line[i+4:]
		if j := strings.Index(rest, " verdict="); j >= 0 {
			rest = rest[:j]
		}
		ids = append(ids, strings.TrimSpace(rest))
	}
	return ids
}

// TestT07ShadowedImportAlias：局部变量遮蔽导入别名 → 方法身份，不借包函数。
func TestT07ShadowedImportAlias(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t07Fixture(t)
	ids := t07CallIDs(t, dir, "EntryShadow", 12)
	want := "symbol:go:example.com/flow:(S).Run"
	if len(ids) != 1 || ids[0] != want {
		t.Fatalf("EntryShadow 调用身份 = %v，期望 [%s]", ids, want)
	}
	for _, id := range ids {
		if strings.Contains(id, "/other:Run") {
			t.Errorf("遮蔽后不得借包函数 other.Run（%s）", id)
		}
	}
}

// TestT07SameLineCrossPackage：同一行同名跨包调用各自准确身份。
func TestT07SameLineCrossPackage(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t07Fixture(t)
	ids := t07CallIDs(t, dir, "EntryCross", 12)
	sawOther, sawLocal := false, false
	for _, id := range ids {
		switch id {
		case "symbol:go:example.com/flow/other:Do":
			sawOther = true
		case "symbol:go:example.com/flow:Do":
			sawLocal = true
		}
	}
	if !sawOther || !sawLocal {
		t.Errorf("同行同名跨包调用身份不全：%v", ids)
	}
}

// TestT07DepthIdentitySame：depth=1 与完整展开的调用身份一致（只是不再展开）。
func TestT07DepthIdentitySame(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t07Fixture(t)
	for _, entry := range []string{"EntryShadow", "EntryCross"} {
		d1 := t07CallIDs(t, dir, entry, 1)
		d12 := t07CallIDs(t, dir, entry, 12)
		// depth=1 的前 N 条应与完整展开前缀一致（不因 depth 改变身份）。
		if len(d1) > len(d12) {
			t.Errorf("%s：depth1 身份数超过完整展开（%v vs %v）", entry, d1, d12)
			continue
		}
		for i := range d1 {
			if d1[i] != d12[i] {
				t.Errorf("%s：depth=1 第 %d 条身份 %q ≠ 完整展开 %q", entry, i, d1[i], d12[i])
			}
		}
	}
}

// TestT07UnknownInstanceNoDefiniteExec：单实现 + 未知实例 → 不产生确定执行。
func TestT07UnknownInstanceNoDefiniteExec(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t07Fixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:EntryUnknown",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, "unknown_receiver") {
		t.Errorf("单实现但未知实例应 unknown_receiver\n%s", out)
	}
	if strings.Contains(out, "flow:HitA verdict=") {
		t.Errorf("未知实例不应执行 HitA\n%s", out)
	}
}
