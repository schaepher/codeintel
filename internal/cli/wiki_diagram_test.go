package cli

// R99 测试（用户）：plantuml 转换失败 → 立即停止并报错（不降级文本
// 块）；渲染入口检查 renderErr 后中止（不产出部分成功的 wiki）。

import (
	"errors"
	"strings"
	"testing"
)

// TestDiagramPlantumlFailFast：plantuml 渲染失败 → 图块返回空 +
// renderErr 记录 + renderWikiHTML/renderWiki 中止。
func TestDiagramPlantumlFailFast(t *testing.T) {
	old := plantumlRenderFunc
	plantumlRenderFunc = func(string) ([]byte, error) { return nil, errors.New("模拟转换失败") }
	defer func() { plantumlRenderFunc = old }()

	rc := &wikiRenderCtx{Diagram: "plantuml"}
	// md 图块：plantuml 命令失败 → 空（不降级文本块）
	if got := rc.diagramMD("graph LR\n  A --> B\n"); got != "" {
		t.Errorf("diagramMD 失败应返回空（不降级），got %q", got)
	}
	// html 图块：失败 → 空
	if got := rc.diagramHTML("graph LR\n  A --> B\n"); got != "" {
		t.Errorf("diagramHTML 失败应返回空（不降级），got %q", got)
	}
	// renderErr 已记录（首个错误）
	if rc.renderErr == nil || !strings.Contains(rc.renderErr.Error(), "模拟转换失败") {
		t.Fatalf("renderErr = %v; want 记录转换失败", rc.renderErr)
	}
	// 渲染入口立即中止（返回错误，不写文件）
	if err := renderWikiHTML("", t.TempDir(), rc); err == nil {
		t.Error("renderWikiHTML 在 renderErr 时应返回错误（中止）")
	}
	rc2 := &wikiRenderCtx{Diagram: "plantuml"}
	rc2.diagramHTML("graph LR\n  A --> B\n")
	if err := renderWiki("", t.TempDir(), rc2); err == nil {
		t.Error("renderWiki 在 renderErr 时应返回错误（中止）")
	}
	// mermaid→plantuml 转换失败（不支持的图类型）也即停
	rc3 := &wikiRenderCtx{Diagram: "plantuml"}
	if got := rc3.diagramHTML("flowchart LR\n  A --> B\n"); got != "" {
		t.Errorf("转换失败也应返回空，got %q", got)
	}
	if rc3.renderErr == nil || !strings.Contains(rc3.renderErr.Error(), "转换失败") {
		t.Errorf("转换失败应记录 renderErr: %v", rc3.renderErr)
	}
}
