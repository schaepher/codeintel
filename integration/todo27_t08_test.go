//go:build integration

package integration

// todo27 T08：场景常量的源码身份与转换边界（真实 init → CLI）。
//
// 覆盖：跨包 iota 常量 + `int32(…)` 合法转换 → taken；局部 const 别名；
// 越界/未知值转换、被遮蔽的 int32、未知左值的短路 → 保持 unknown（两分支）。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t08FlowSrc = `package flow
import "example.com/flow/pb"
func Hit(){}
func Miss(){}
func Entry(sourceType int32){if sourceType==int32(pb.Client){Hit()}else{Miss()}}
func EntryIota(sourceType int32){if sourceType==int32(pb.SourceD){Hit()}else{Miss()}}
func EntryAlias(sourceType int32){const local=pb.SourceB;if sourceType==int32(local){Hit()}else{Miss()}}
var big = int64(1)<<40
func EntryOverflow(sourceType int32){if sourceType==int32(big){Hit()}else{Miss()}}
func EntryShadow(sourceType int32){int32:=func(v pb.ClientType)int32{return 0};if sourceType==int32(pb.Client){Hit()}else{Miss()}}
func EntryUnknownLeft(sourceType int32,cond func()bool){if cond()&&sourceType==4{Hit()}else{Miss()}}
func EntryLit(sourceType int32){if sourceType==0x4{Hit()}else{Miss()}}
`

const t08PBSrc = `package pb
type ClientType int32
const (
 Client ClientType = iota
 Other
 SourceB
 SourceC
 SourceD
)
`

func t08Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t08FlowSrc)
	writeFile(t, filepath.Join(dir, "pb", "pb.go"), t08PBSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func t08Seq(t *testing.T, dir, entry, scene string) string {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid", "--scene", scene)
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	return out
}

// TestT08ConstConversions：跨包 iota/别名/十六进制常量 → 场景 taken。
func TestT08ConstConversions(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t08Fixture(t)
	for _, tc := range []struct{ entry, scene string }{
		{"Entry", "sourceType=0"},      // int32(pb.Client) == 0
		{"EntryIota", "sourceType=4"},  // int32(pb.SourceD) == 4
		{"EntryAlias", "sourceType=2"}, // 局部 const local = pb.SourceB
		{"EntryLit", "sourceType=4"},   // 0x4 归一化
	} {
		out := t08Seq(t, dir, tc.entry, tc.scene)
		if !strings.Contains(out, "场景判定 taken") {
			t.Errorf("%s [%s] 应判定 taken\n%s", tc.entry, tc.scene, out)
		}
		if strings.Contains(out, "flow:Miss verdict=") {
			t.Errorf("%s [%s] 排除分支不应执行 Miss\n%s", tc.entry, tc.scene, out)
		}
	}
}

// TestT08UnknownBoundaries：越界/未知值转换、遮蔽 int32、未知左值短路 →
// 两分支保留（不因右侧可知升级）。
func TestT08UnknownBoundaries(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t08Fixture(t)
	cases := []struct{ entry, scene string }{
		{"EntryOverflow", "sourceType=0"},
		{"EntryShadow", "sourceType=0"},
		{"EntryUnknownLeft", "sourceType=4"},
	}
	for _, c := range cases {
		out := t08Seq(t, dir, c.entry, c.scene)
		if !strings.Contains(out, "flow:Hit verdict=") || !strings.Contains(out, "flow:Miss verdict=") {
			t.Errorf("%s [%s] 应保留两个分支（未知条件）\n%s", c.entry, c.scene, out)
		}
	}
}
