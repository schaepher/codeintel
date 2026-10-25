package cli

// todo10-2 渲染层：defer/go/return 的语义必须在用户可见输出里可区分——
// defer 不得呈现为声明处立即执行；go 不得呈现为同步完成；return 带返回值。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const seqSemanticsCLISrc = `package m

func unlock() {}

func handle() {}

func audit() {}

func call() string { return "ok" }

func wrap() (resp string, err error) {
	defer unlock()
	go handle()
	resp = call()
	audit()
	return resp, nil
}
`

// seedSeqSemanticsCLI 建 fixture（wrap 具名结果）并写入索引。
func seedSeqSemanticsCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(seqSemanticsCLISrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := sqlite.NewRepo(db).SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:wrap", Kind: domain.KindFunction, Name: "wrap", FilePath: "main.go", LineStart: 11},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSequenceCodeMermaidDeferGoSemantics：mermaid 里 defer 有 "defer:" 前缀、
// go 用异步箭头并注明完成顺序未知（不画同步返回线）。
func TestSequenceCodeMermaidDeferGoSemantics(t *testing.T) {
	dir := seedSeqSemanticsCLI(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "example.com/m:wrap", "--code", "--format", "mermaid", "--repo", dir}); code != 0 {
			t.Errorf("sequence --code --format mermaid exit = %d", code)
		}
	})
	if !strings.Contains(out, ": defer unlock") {
		t.Errorf("mermaid 应标出 defer 语义（消息前缀 defer）:\n%s", out)
	}
	if !strings.Contains(out, "-)") {
		t.Errorf("go 调用应用异步箭头 -):\n%s", out)
	}
	if !strings.Contains(out, ": go handle") {
		t.Errorf("mermaid 应标出 go 语义（消息前缀 go）:\n%s", out)
	}
	if !strings.Contains(out, "并发启动（完成顺序未知）") {
		t.Errorf("go 调用应注明完成顺序未知:\n%s", out)
	}
	// return 线带返回值（wrap 的返回在 return 节点渲染）
	if !strings.Contains(out, "return resp, nil") {
		t.Errorf("mermaid 应渲染 return 节点（含返回值）:\n%s", out)
	}
}

// TestSequenceCodeTextSemantics：文本里 defer/go 前缀可见；return 显示返回
// 表达式与结果槽。
func TestSequenceCodeTextSemantics(t *testing.T) {
	dir := seedSeqSemanticsCLI(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "example.com/m:wrap", "--code", "--repo", dir}); code != 0 {
			t.Errorf("sequence --code exit = %d", code)
		}
	})
	for _, want := range []string{"defer unlock", "go handle", "return resp, nil"} {
		if !strings.Contains(out, want) {
			t.Errorf("文本输出缺 %q:\n%s", want, out)
		}
	}
	// 具名结果槽：返回值与槽名相同时不重复标注
	if strings.Contains(out, "[slots: resp, nil]") {
		t.Errorf("槽名与值文本重复时不应重复标注:\n%s", out)
	}
}

// TestSequenceCodeFocusFlag：--focus 参数生效（只留关注目标），并在
// meta.focus 标 applied；未命中保留全量 + applied=false。
func TestSequenceCodeFocusFlag(t *testing.T) {
	dir := seedSeqSemanticsCLI(t)
	out := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "example.com/m:wrap", "--code", "--json", "--focus", "return", "--repo", dir}); code != 0 {
			t.Errorf("--focus return exit = %d", code)
		}
	})
	if !strings.Contains(out, `"focus"`) || !strings.Contains(out, `"applied": true`) {
		t.Errorf("--focus 生效时 JSON 应含 meta.focus.applied=true:\n%s", out)
	}
	if strings.Contains(out, `"assign"`) {
		t.Errorf("--focus return 不应输出无关赋值步骤:\n%s", out)
	}

	out = captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "example.com/m:wrap", "--code", "--json", "--focus", "No.Such", "--repo", dir}); code != 0 {
			t.Errorf("--focus 未命中 exit = %d", code)
		}
	})
	if !strings.Contains(out, `"applied": false`) {
		t.Errorf("未命中时应保留全量并标 applied=false:\n%s", out)
	}
}
