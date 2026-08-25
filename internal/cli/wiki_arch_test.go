package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestArchMermaidFallback：R2——yaml architecture 空时自动包间调用
// 聚合图（同 from→to 计数相加，确定性排序）。
func TestArchMermaidFallback(t *testing.T) {
	data := []*domain.WikiModule{
		{Name: "m1", PkgCalls: []*domain.WikiPkgCall{
			{From: "cli", To: "action", Count: 5},
			{From: "cli", To: "server", Count: 1},
		}},
		{Name: "m2", PkgCalls: []*domain.WikiPkgCall{
			{From: "cli", To: "action", Count: 3},
		}},
	}
	got := archMermaidFallback(data, nil)
	for _, want := range []string{"cli[cli] -->|8| action[action]", "cli[cli] -->|1| server[server]"} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback 应含 %q:\n%s", want, got)
		}
	}
	if archMermaidFallback(nil, nil) != "" {
		t.Errorf("空数据 fallback 应为空")
	}
}

// TestArchMermaidFallbackFullPathPackages：R42——domains.packages 完整
// 路径（R38 起 AI 输出）→ 领域聚合仍生效（末段短名匹配——PkgCalls
// 的 From/To 是包短名）。
func TestArchMermaidFallbackFullPathPackages(t *testing.T) {
	data := []*domain.WikiModule{
		{Name: "m1", PkgCalls: []*domain.WikiPkgCall{
			{From: "order", To: "member", Count: 7},
			{From: "order", To: "item", Count: 3},
		}},
	}
	doms := []wikiDomainCfg{
		{Name: "交易域", Packages: []string{"github.com/ixre/go2o/internal/impl/domain/order"}},
		{Name: "会员域", Packages: []string{"github.com/ixre/go2o/internal/impl/domain/member"}},
		{Name: "商品域", Packages: []string{"github.com/ixre/go2o/internal/impl/domain/item"}},
	}
	got := archMermaidFallback(data, doms)
	for _, want := range []string{`D交易域["交易域（1 包）"]`, `D会员域["会员域（1 包）"]`, "D交易域 -->|7| D会员域", "D交易域 -->|3| D商品域"} {
		if !strings.Contains(got, want) {
			t.Errorf("完整路径 packages 领域聚合应含 %q:\n%s", want, got)
		}
	}
}

// TestMergeTableColumnsHidden：R3——yaml 列 hidden 同时过滤自动列
// （解析噪音列：别名列错误归属产生的表.列虚拟节点）。
func TestMergeTableColumnsHidden(t *testing.T) {
	cols := []*domain.TableColumn{
		{Name: "edges.name", ColType: "TEXT"},
		{Name: "edges.id", ColType: "INTEGER"},
	}
	yamlCols := []wikiTableColumn{
		{Name: "id", Comment: "自增主键"},
		{Name: "name", Comment: "噪音", Hidden: true},
	}
	rows := mergeTableColumns("edges", cols, yamlCols)
	for _, r := range rows {
		if r.name == "name" {
			t.Errorf("hidden 列 name 不应渲染: %+v", rows)
		}
	}
	if len(rows) != 1 || rows[0].name != "id" {
		t.Errorf("rows = %+v, want 仅 id", rows)
	}
}

// TestExtractEnums：R5——枚举提取（类型化/字符串 const + 注释 +
// 长文本过滤 + 测试文件排除）。
func TestExtractEnums(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "internal", "demo", "kinds.go"), `package demo

// EdgeKind 边类型。
type EdgeKind string

const (
	EdgeCalls  EdgeKind = "calls"
	EdgeAlias  EdgeKind = "alias" // 指针别名
	LongText            = "这是一个非常长的文本常量，用来测试长文本过滤逻辑是否正确工作，应该被过滤掉不当作枚举展示出来"
)

// 无类型常量（展示标签等）——默认过滤，--include-untyped 放开
const (
	StatusOK    = "ok"
	StatusFail  = "fail"
)
`)
	entries := extractEnums(dir, true)
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name] = true
		if e.Name == "EdgeCalls" {
			if e.Type != "EdgeKind" || e.Value != "calls" || e.Pkg != "demo" {
				t.Errorf("EdgeCalls 提取 = %+v", e)
			}
		}
		if e.Name == "EdgeAlias" && e.Comment != "指针别名" {
			t.Errorf("EdgeAlias 注释 = %q", e.Comment)
		}
	}
	if !found["EdgeCalls"] || !found["EdgeAlias"] {
		t.Errorf("应提取 EdgeCalls/EdgeAlias: %v", found)
	}
	if found["LongText"] {
		t.Errorf("长文本常量不应提取: %v", found)
	}
	// R6：默认过滤无类型常量（StatusOK 无显式类型）
	if found["StatusOK"] {
		t.Errorf("无类型常量默认应过滤: %v", found)
	}
	// --include-untyped 放开
	all := extractEnums(dir, false)
	foundAll := map[string]bool{}
	for _, e := range all {
		foundAll[e.Name] = true
	}
	if !foundAll["StatusOK"] || !foundAll["EdgeCalls"] {
		t.Errorf("include-untyped 应含无类型常量: %v", foundAll)
	}
}

// TestArchMermaidCurated：R7——AI 整理图：过滤基础包（logging）+ 临时
// 包（seed）+ 分层 subgraph 分组；未分组包边丢弃。
func TestArchMermaidCurated(t *testing.T) {
	data := []*domain.WikiModule{
		{Name: "m1", PkgCalls: []*domain.WikiPkgCall{
			{From: "cli", To: "action", Count: 5},
			{From: "cli", To: "logging", Count: 10},    // 基础包过滤
			{From: "seed", To: "sqlite", Count: 4},     // 临时包过滤
			{From: "cli", To: "unknown_pkg", Count: 1}, // 未分组丢弃
			{From: "domain", To: "canonicalizer", Count: 2},
		}},
	}
	got := archMermaidCurated(data)
	for _, want := range []string{"subgraph 入口层", "subgraph 核心层", "subgraph 支撑层", "cli[cli] -->|5| action[action]"} {
		if !strings.Contains(got, want) {
			t.Errorf("curated 应含 %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"logging", "seed", "unknown_pkg"} {
		if strings.Contains(got, bad) {
			t.Errorf("curated 不应含 %q:\n%s", bad, got)
		}
	}
}

// TestArchMermaidCuratedUnrecognized：R42——分组规则不识别该项目
// （go2o 实测：包名不在硬编码分层里，只剩 domain 1 包 1 自环边）→
// 有效节点 < 3 降级返回空（不显示贫瘠的 AI 整理版，保留自动聚合版）。
func TestArchMermaidCuratedUnrecognized(t *testing.T) {
	data := []*domain.WikiModule{
		{Name: "m1", PkgCalls: []*domain.WikiPkgCall{
			{From: "domain", To: "domain", Count: 2}, // 仅命中"支撑层"的 domain
			{From: "app", To: "infra", Count: 9},     // 未分组包——边丢弃
		}},
	}
	if got := archMermaidCurated(data); got != "" {
		t.Errorf("不识别项目 curated 应降级为空（有效节点 <3）:\n%s", got)
	}
}
