package cli

// R100 待办12：config merge 多层嵌套——extractTemplateBlock/insertBlock
// 原实现硬编码 2 层（3 层键如 ai.fill.modules 会整块返回 fill 全部子键 /
// 插到顶层）。改为按 keyPath 逐层下钻。

import (
	"strings"
	"testing"
)

// TestExtractTemplateBlockNested：3 层键精确提取（只含目标键 + 前导
// 注释——不含兄弟键）。
func TestExtractTemplateBlockNested(t *testing.T) {
	tmpl := `# AI 补缺配置
ai:
  domains: auto
  # 增量补缺类别开关
  fill:
    # 模块描述
    modules: auto
    tables: auto
    glossary: auto
  ask: auto
`
	block := extractTemplateBlock(tmpl, []string{"ai", "fill", "modules"})
	want := "# 模块描述\n    modules: auto"
	if strings.TrimSpace(block) != strings.TrimSpace(want) {
		t.Errorf("3 层提取 = %q; want %q", block, want)
	}
	if strings.Contains(block, "tables") || strings.Contains(block, "glossary") {
		t.Errorf("3 层提取不应包含兄弟键（原实现整块返回）:\n%s", block)
	}
}

// TestExtractTemplateBlockTwoLevel：2 层键保持原行为（含前导注释）。
func TestExtractTemplateBlockTwoLevel(t *testing.T) {
	tmpl := `# 时序图配置
seq:
  stop_packages: []
  # 过滤包
  filter_pkgs: []
`
	block := extractTemplateBlock(tmpl, []string{"seq", "filter_pkgs"})
	want := "# 过滤包\n  filter_pkgs: []"
	if strings.TrimSpace(block) != strings.TrimSpace(want) {
		t.Errorf("2 层提取 = %q; want %q", block, want)
	}
}

// TestExtractTemplateBlockTop：顶层键含前导注释（1 层）。
func TestExtractTemplateBlockTop(t *testing.T) {
	tmpl := `# AI 补缺配置
ai:
  domains: auto
`
	block := extractTemplateBlock(tmpl, []string{"ai"})
	if !strings.Contains(block, "# AI 补缺配置") || !strings.Contains(block, "ai:") {
		t.Errorf("顶层提取应含前导注释:\n%s", block)
	}
}

// TestInsertBlockNested：3 层键插入父块内（ai.fill 块末尾——不是整块
// 追加到 ai 下）。
func TestInsertBlockNested(t *testing.T) {
	existing := `ai:
  domains: auto
  fill:
    tables: auto
seq:
  stop_packages: []
`
	lines := strings.Split(existing, "\n")
	lines = insertBlock(lines, []string{"ai", "fill", "modules"}, "    # 模块描述\n    modules: auto")
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "fill:\n    tables: auto\n    # 模块描述\n    modules: auto") {
		t.Errorf("3 层插入应在 fill 块内:\n%s", out)
	}
	// 后续顶层键保留
	if !strings.Contains(out, "seq:\n  stop_packages: []") {
		t.Errorf("插入不应破坏后续顶层键:\n%s", out)
	}
}
