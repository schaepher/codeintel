package cli

// F1 遗留修复：processes 页不再硬编码 codeintel 自身命令（go2o 验收
// 暴露：12 个 codeintel 命令在目标索引全无调用链——wiki-check 时序
// FAIL），改为目标仓库 main 入口生成（对齐 commands 页 F1 方案）。
// 测试先行。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestRenderProcessesFromEntry：processes 页从目标仓库 main 入口生成
// ——不再含 codeintel 自身命令（init/update/serve/...），改为主入口 +
// 一级调用展开。
func TestRenderProcessesFromEntry(t *testing.T) {
	dir := seedRepo(t) // fixture: main → (Svc).Run（example.com/m）
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))

	m := renderProcessesMD(acts)
	// 不再硬编码 codeintel 自身命令
	for _, bad := range []string{"init —— 全量构建索引", "cmdInit", "cmdWiki", "cmdServe", "codeintel"} {
		if strings.Contains(m, bad) {
			t.Errorf("processes 页不应含 codeintel 自身命令 %q:\n%s", bad, m)
		}
	}
	// 改为目标仓库 main 入口 + 一级调用
	for _, want := range []string{"# 系统流程", "## 入口 `main`", "main.go", "(Svc).Run"} {
		if !strings.Contains(m, want) {
			t.Errorf("processes 页应含 %q:\n%s", want, m)
		}
	}

	h := renderProcessesHTML(acts)
	for _, want := range []string{`<h3>入口 <code>main</code>`, "<code>svc:(Svc).Run</code>"} {
		if !strings.Contains(h, want) {
			t.Errorf("processes html 应含 %q:\n%s", want, h)
		}
	}
	for _, bad := range []string{"init —— 全量构建索引", "cmdInit"} {
		if strings.Contains(h, bad) {
			t.Errorf("processes html 不应含 codeintel 自身命令 %q:\n%s", bad, h)
		}
	}
}
