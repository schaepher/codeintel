package action

// todo22 §6：`SymbolSource`——按 canonical ID 精确输出函数/方法声明源码。
//
// 反例矩阵（每条都必须明确失败、不返回部分成功）：
//   - 非 canonical 输入（不走模糊搜索）
//   - ID 不存在 / 虚拟节点（无文件）/ 不支持的声明种类
//   - 缺文件、路径穿越、symlink 逃逸、解析失败
//   - 包身份与最近 go.mod 不符、receiver/函数名在文件内匹配不到、多重声明
//
// 正例：指针接收者、泛型接收者、同名不同 receiver（不能串线）、陈旧索引行号
// （必须按 AST 实际位置取，不用索引里的 LineStart）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const symbolSourceFlowGo = `package src

type A struct{}

type B struct{}

func (a *A) Run() string { return "a" }

func (b *B) Run() string { return "b" }

type Box[T any] struct{ v T }

type Store struct{ DB int }

func (b *Box[T]) Get() T { return b.v }

func Setup() {
	// setup body
}
`

// seedSymbolSourceRepo 写真实源文件 + 索引节点（节点的行号故意写错，用于
// 证明输出按 AST 实际位置而不是索引行号）。
func seedSymbolSourceRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/src\n\ngo 1.21\n")
	write("flow.go", symbolSourceFlowGo)
	write("dup.go", "package src\n\nfunc Dup() {}\n\nfunc Dup() {}\n")
	write("bad.go", "package src\n\nfunc {\n")

	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/src:(A).Run", Kind: domain.KindMethod, Name: "(A).Run",
			FilePath: "flow.go", LineStart: 999}, // 陈旧行号
		{ID: "symbol:go:example.com/src:(B).Run", Kind: domain.KindMethod, Name: "(B).Run",
			FilePath: "flow.go", LineStart: 999},
		{ID: "symbol:go:example.com/src:(Box).Get", Kind: domain.KindMethod, Name: "(Box).Get",
			FilePath: "flow.go", LineStart: 999},
		{ID: "symbol:go:example.com/src:Setup", Kind: domain.KindFunction, Name: "Setup",
			FilePath: "flow.go", LineStart: 999},
		{ID: "symbol:go:example.com/src:Store", Kind: domain.KindStruct, Name: "Store",
			FilePath: "flow.go", LineStart: 1},
		{ID: "symbol:go:example.com/src:Virtual", Kind: domain.KindFunction, Name: "Virtual"},
		{ID: "symbol:go:example.com/src:Missing", Kind: domain.KindFunction, Name: "Missing",
			FilePath: "nope.go", LineStart: 1},
		{ID: "symbol:go:example.com/src:(C).Run", Kind: domain.KindMethod, Name: "(C).Run",
			FilePath: "flow.go", LineStart: 1},
		{ID: "symbol:go:example.com/src:Dup", Kind: domain.KindFunction, Name: "Dup",
			FilePath: "dup.go", LineStart: 1},
		{ID: "symbol:go:example.com/src:Broken", Kind: domain.KindFunction, Name: "Broken",
			FilePath: "bad.go", LineStart: 1},
		{ID: "symbol:go:example.com/src:Escape", Kind: domain.KindFunction, Name: "Escape",
			FilePath: "../outside.go", LineStart: 1},
		{ID: "symbol:go:example.com/src:Link", Kind: domain.KindFunction, Name: "Link",
			FilePath: "link.go", LineStart: 1},
		{ID: "symbol:go:example.com/other:Setup", Kind: domain.KindFunction, Name: "Setup",
			FilePath: "flow.go", LineStart: 1},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	// symlink 逃逸：仓库内 link.go 指向仓库外文件。
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package src\n\nfunc Link() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.go")); err != nil {
		t.Skipf("symlink 不可用: %v", err)
	}
	return New(r), dir
}

// TestSymbolSourceExactTexts：正例——按 AST 实际位置与字节取全文（忽略索引行号）。
func TestSymbolSourceExactTexts(t *testing.T) {
	a, dir := seedSymbolSourceRepo(t)
	cases := []struct {
		id   string
		want string // 声明首行前缀
	}{
		{"symbol:go:example.com/src:(A).Run", "func (a *A) Run() string { return \"a\" }"},
		{"symbol:go:example.com/src:(B).Run", "func (b *B) Run() string { return \"b\" }"},
		{"symbol:go:example.com/src:(Box).Get", "func (b *Box[T]) Get() T { return b.v }"},
		{"symbol:go:example.com/src:Setup", "func Setup() {"},
		{"symbol:go:example.com/src:Store", "type Store struct{"},
	}
	for _, c := range cases {
		got, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: c.id})
		if err != nil {
			t.Errorf("%s: %v", c.id, err)
			continue
		}
		if got.SymbolID != c.id {
			t.Errorf("%s: symbol_id = %q", c.id, got.SymbolID)
		}
		if got.File != "flow.go" {
			t.Errorf("%s: file = %q，期望 flow.go", c.id, got.File)
		}
		if !strings.HasPrefix(got.Text, c.want) {
			t.Errorf("%s: text 首行 %q，期望以 %q 开头", c.id, strings.SplitN(got.Text, "\n", 2)[0], c.want)
		}
		// 行号必须是 AST 实际位置（索引里写的是 999）
		wantLine := 0
		for i, line := range strings.Split(symbolSourceFlowGo, "\n") {
			if strings.HasPrefix(line, strings.SplitN(c.want, " {", 2)[0]) {
				wantLine = i + 1
				break
			}
		}
		if got.LineStart != wantLine {
			t.Errorf("%s: line_start = %d，期望 %d（AST 实际位置，不是索引行号 999）",
				c.id, got.LineStart, wantLine)
		}
		if got.LineEnd < got.LineStart {
			t.Errorf("%s: line_end = %d < line_start = %d", c.id, got.LineEnd, got.LineStart)
		}
		// 全文必须能从源文件按字节区间原样取回（含换行与缩进）
		full, err := os.ReadFile(filepath.Join(dir, got.File))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(full), got.Text) {
			t.Errorf("%s: text 不在源文件字节内（越界切片？）: %q", c.id, got.Text)
		}
	}
	// 同名不同 receiver 不能串线
	aRun, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:(A).Run"})
	if err != nil {
		t.Fatal(err)
	}
	bRun, err := a.SymbolSource(SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:(B).Run"})
	if err != nil {
		t.Fatal(err)
	}
	if aRun.Text == bRun.Text {
		t.Errorf("(A).Run 与 (B).Run 取了同一段源码: %q", aRun.Text)
	}
}

// TestSymbolSourceFailures：每条反例都必须明确失败（结果为空），错误可区分。
func TestSymbolSourceFailures(t *testing.T) {
	a, dir := seedSymbolSourceRepo(t)
	cases := []struct {
		name string
		req  SymbolSourceRequest
		want string // 错误信息必须包含的关键词
	}{
		{"非 canonical 输入", SymbolSourceRequest{RepoAbs: dir, SymbolID: "Run"}, "canonical"},
		{"ID 不存在", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:Nope"}, "不存在"},
		{"虚拟节点(无文件)", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:Virtual"}, "文件"},
		{"缺文件", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:Missing"}, "文件"},
		{"receiver 不匹配", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:(C).Run"}, "匹配"},
		{"多重声明", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:Dup"}, "多重"},
		{"解析失败", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:Broken"}, "解析"},
		{"路径穿越", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:Escape"}, "仓库"},
		{"symlink 逃逸", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/src:Link"}, "仓库"},
		{"包身份不符", SymbolSourceRequest{RepoAbs: dir, SymbolID: "symbol:go:example.com/other:Setup"}, "包身份"},
	}
	for _, c := range cases {
		got, err := a.SymbolSource(c.req)
		if err == nil {
			t.Errorf("%s: 应失败，got %+v", c.name, got)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: 错误信息 %q 应包含 %q", c.name, err.Error(), c.want)
		}
		if got != nil {
			t.Errorf("%s: 失败时不得返回部分结果: %+v", c.name, got)
		}
	}
}
