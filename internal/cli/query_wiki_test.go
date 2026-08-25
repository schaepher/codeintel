package cli

// R77 测试：wiki 特性转命令——query packages / architecture / er /
// module（数据函数与 wiki 渲染同源——wiki 用的数据命令行可查）。

import (
	"strings"
	"testing"
)

// TestQueryPackagesCmd：包结构输出含 doc_comment（去 Copyright）。
func TestQueryPackagesCmd(t *testing.T) {
	dir := seedWikiRepo(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"packages", "--repo", dir}); code != 0 {
			t.Fatalf("packages exit = %d", code)
		}
	})
	for _, want := range []string{"主包：业务入口", "服务层"} {
		if !strings.Contains(out, want) {
			t.Errorf("packages 应含 %q:\n%s", want, out)
		}
	}
}

// TestQueryPackagesJSON：--json 输出含 doc 字段。
func TestQueryPackagesJSON(t *testing.T) {
	dir := seedWikiRepo(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"packages", "--repo", dir, "--json"}); code != 0 {
			t.Fatalf("packages --json exit = %d", code)
		}
	})
	for _, want := range []string{`"path"`, `"doc"`, "主包：业务入口"} {
		if !strings.Contains(out, want) {
			t.Errorf("packages --json 应含 %q:\n%s", want, out)
		}
	}
}

// TestQueryArchitectureCmd：架构图输出 mermaid（--format mermaid）。
func TestQueryArchitectureCmd(t *testing.T) {
	dir := seedWikiRepo(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"architecture", "--repo", dir, "--format", "mermaid"}); code != 0 {
			t.Fatalf("architecture exit = %d", code)
		}
	})
	if !strings.Contains(out, "subgraph") {
		t.Errorf("架构图应含 subgraph 分层:\n%s", out)
	}
}

// TestQueryArchitectureJSON：--json 含 modules 计数与 mermaid 文本。
func TestQueryArchitectureJSON(t *testing.T) {
	dir := seedWikiRepo(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"architecture", "--repo", dir, "--json"}); code != 0 {
			t.Fatalf("architecture --json exit = %d", code)
		}
	})
	for _, want := range []string{`"modules"`, `"mermaid"`} {
		if !strings.Contains(out, want) {
			t.Errorf("architecture --json 应含 %q:\n%s", want, out)
		}
	}
}

// TestQueryERCmd：ER 图输出 erDiagram（fk/query 直接键关联）。
func TestQueryERCmd(t *testing.T) {
	dir := seedTablePathFixture(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"er", "--repo", dir, "--format", "mermaid"}); code != 0 {
			t.Fatalf("er exit = %d", code)
		}
	})
	if !strings.Contains(out, "erDiagram") {
		t.Errorf("ER 图应含 erDiagram:\n%s", out)
	}
	if !strings.Contains(out, "table_a") {
		t.Errorf("ER 图应含 table_a（fk 链端点）:\n%s", out)
	}
}

// TestQueryERDefault：默认文本输出关系明细行（from.col → to.col）。
func TestQueryERDefault(t *testing.T) {
	dir := seedTablePathFixture(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"er", "--repo", dir}); code != 0 {
			t.Fatalf("er exit = %d", code)
		}
	})
	if !strings.Contains(out, "table_map") {
		t.Errorf("ER 默认文本应含 table_map（fk 链中间表）:\n%s", out)
	}
}

// TestQueryModuleCmd：模块详情含核心符号/相关表/关键数据流。
func TestQueryModuleCmd(t *testing.T) {
	dir := seedWikiRepo(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"module", "m", "--repo", dir}); code != 0 {
			t.Fatalf("module exit = %d", code)
		}
	})
	for _, want := range []string{"example.com/m", "main", "orders"} {
		if !strings.Contains(out, want) {
			t.Errorf("module 应含 %q:\n%s", want, out)
		}
	}
}

// TestQueryModuleMissing：不存在的模块报错退出。
func TestQueryModuleMissing(t *testing.T) {
	dir := seedWikiRepo(t)
	code := cmdQuery([]string{"module", "notexist", "--repo", dir})
	if code == 0 {
		t.Error("module 不存在应非零退出")
	}
}
