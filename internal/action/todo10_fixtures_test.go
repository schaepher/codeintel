package action

// todo10-8：流程证据 + 脱敏回归夹具。
//
//   - 四类中性夹具（testdata/todo10/{wrap_status,cross_entry_async,
//     dual_advance,partial_write}）：包装函数改返回状态 / 跨审核+定时任务
//     推进 / 人工与内部自动推进并存 / 主写入+追踪记录部分成功
//   - 夹具脱敏门禁：模块名中性、无私有域名/仓库名、源码可解析
//   - 箭头证据身份：canonical ID + 源码位置 + 可复现查询命令 + 支持程度
//     （direct/corroborated/unverified）——结构化结果与图形展示同一身份
//
// 说明：夹具 2/3/4 的断言依赖 todo10-6/7（异步关联、副作用摘要），本轮先
// 固化夹具与"语句语义/终点诊断/精确方法匹配"层面的证据（用户约定的顺序）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// todo10FixtureDirs 四类夹具目录（相对本测试文件）。
var todo10FixtureDirs = []string{"wrap_status", "cross_entry_async", "dual_advance", "partial_write"}

// forbiddenFixturePatterns 脱敏门禁：私有仓库/公司/线上依赖的特征串。
var forbiddenFixturePatterns = []string{
	"github.com/", "gitee.com/", "gitlab.", "go2o", "ixre", "gof/", "gof\"",
	"mch_", "pay_", "merchant", "https://", "http://", "internal.",
}

// TestTodo10FixturesSanitized：四个夹具存在、模块名中性、可解析、无私有特征。
func TestTodo10FixturesSanitized(t *testing.T) {
	for _, name := range todo10FixtureDirs {
		dir := filepath.Join("testdata", "todo10", name)
		modPath := filepath.Join(dir, "go.mod")
		b, err := os.ReadFile(modPath)
		if err != nil {
			t.Errorf("%s: 缺 go.mod: %v", name, err)
			continue
		}
		wantMod := "module example.com/fx/" + name
		if !strings.Contains(string(b), wantMod) {
			t.Errorf("%s: go.mod 模块名应为 %q（中性）: %s", name, wantMod, b)
		}
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		if len(files) == 0 {
			t.Errorf("%s: 无 Go 源文件", name)
			continue
		}
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parser.ParseFile(token.NewFileSet(), f, src, 0); err != nil {
				t.Errorf("%s: 源码解析失败: %v", f, err)
			}
			lower := strings.ToLower(string(src))
			for _, bad := range forbiddenFixturePatterns {
				if strings.Contains(lower, strings.ToLower(bad)) {
					t.Errorf("%s: 含私有/线上特征 %q（夹具必须脱敏）", f, bad)
				}
			}
		}
	}
}

// indexTodo10Fixture 用 go/parser 建最小索引（节点 + 同包 calls 边）——
// 夹具测试不依赖 scip-go，也不写入仓库目录（先复制到临时目录）。
// 返回 (标签仓库目录, actions)。
func indexTodo10Fixture(t *testing.T, name string) (string, *Actions) {
	t.Helper()
	srcDir := filepath.Join("testdata", "todo10", name)
	dst := t.TempDir()
	for _, fn := range []string{"go.mod", "flow.go"} {
		b, err := os.ReadFile(filepath.Join(srcDir, fn))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dst, fn), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(dst, "flow.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	modPath := "example.com/fx/" + name
	idOf := func(recv, name string) string {
		if recv != "" {
			return "symbol:go:" + modPath + ":(" + recv + ")." + name
		}
		return "symbol:go:" + modPath + ":" + name
	}
	// 1) 节点 + 方法接收者
	type fnInfo struct {
		id   string
		name string
		recv string
		line int
		body *ast.BlockStmt
		decl *ast.FuncDecl
	}
	var fns []fnInfo
	all := map[string]string{} // 短名/方法名 → ID（同包解析）
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		recv := ""
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			recv = strings.TrimPrefix(strings.TrimPrefix(exprTypeName(fd.Recv.List[0].Type), ""), "")
			recv = strings.TrimLeft(exprTypeName(fd.Recv.List[0].Type), "*")
		}
		id := idOf(recv, fd.Name.Name)
		fns = append(fns, fnInfo{id: id, name: fd.Name.Name, recv: recv, line: fset.Position(fd.Pos()).Line, body: fd.Body, decl: fd})
		if recv == "" {
			all[fd.Name.Name] = id
		} else {
			all[fd.Name.Name] = id // 方法按短名兜底（夹具无重名）
		}
	}
	nodes := make([]*domain.CodeEntity, 0, len(fns))
	for _, fn := range fns {
		kind := domain.KindFunction
		if fn.recv != "" {
			kind = domain.KindMethod
		}
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(fn.id), Kind: kind,
			Name: fn.name, FilePath: "flow.go", LineStart: fn.line})
	}
	// 2) calls 边（同包：Ident / pkg-less SelectorExpr 的 Sel 名）
	var facts []*domain.Fact
	for _, fn := range fns {
		ast.Inspect(fn.body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			target, ok := all[name]
			if !ok || target == fn.id {
				return true
			}
			pos := fset.Position(call.Lparen)
			facts = append(facts, &domain.Fact{
				SourceID: domain.CanonicalID(fn.id), TargetID: domain.CanonicalID(target),
				Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
				Metadata: map[string]any{"line_num": float64(pos.Line), "pos": float64(pos.Offset)},
			})
			return true
		})
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "fx-" + name, ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dst, New(r)
}

// TestWrapStatusFixtureSemantics：包装函数夹具——默认状态与最终返回状态
// 可区分（return 节点带返回值；--focus 只留影响状态字段的步骤）。
func TestWrapStatusFixtureSemantics(t *testing.T) {
	dir, acts := indexTodo10Fixture(t, "wrap_status")

	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/fx/wrap_status:Wrap", RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	// 顶层最后一个节点应是包装函数自己的 return（最终返回状态载体）
	if len(root.Nodes) == 0 {
		t.Fatal("应有时序步骤")
	}
	last := root.Nodes[len(root.Nodes)-1]
	if last.Kind != "return" || len(last.Values) != 1 || last.Values[0] != "out" {
		t.Errorf("顶层末节点应为 return out（最终状态）: %+v", last)
	}
	// 默认状态的来源单独可见（嵌套展开里 DefaultState 的 return "pending"）
	var nested []string
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n.Kind == "return" {
				nested = append(nested, strings.Join(n.Values, ", "))
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)
	all := strings.Join(nested, " | ")
	if !strings.Contains(all, `"pending"`) {
		t.Errorf("默认状态来源应可区分（嵌套 return \"pending\"）: %s", all)
	}
	if !strings.Contains(all, "out") {
		t.Errorf("最终返回状态应可区分（return out）: %s", all)
	}

	// 焦点字段：只保留改写状态那一行 + 相关调用，无关 audit 被剔除
	focused, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "example.com/fx/wrap_status:Wrap", RepoAbs: dir, Depth: 2, Focus: []string{"Resp.State"},
	})
	if err != nil || focused == nil {
		t.Fatalf("focused CodeSequence: %v root=%v", err, focused)
	}
	if focused.Meta == nil || focused.Meta.Focus == nil || !focused.Meta.Focus.Applied {
		t.Fatalf("焦点过滤应生效: %+v", focused.Meta)
	}
	var flat []string
	var walk2 func(nodes []*CodeSeqNode)
	walk2 = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			flat = append(flat, n.Kind+":"+n.Label)
			walk2(n.Nodes)
			walk2(n.Else)
		}
	}
	walk2(focused.Nodes)
	joined := strings.Join(flat, " | ")
	if strings.Contains(joined, "audit") {
		t.Errorf("--focus 不应保留与状态字段无关的调用 audit:\n%s", joined)
	}
	if !strings.Contains(joined, "State") {
		t.Errorf("--focus 应保留状态字段赋值:\n%s", joined)
	}
}

// TestArrowEvidenceIdentity：箭头证据身份——canonical ID + 源码位置 +
// 可复现命令 + 支持程度（结构化结果即图形展示用的同一身份）。
func TestArrowEvidenceIdentity(t *testing.T) {
	dir, acts := indexTodo10Fixture(t, "wrap_status")
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/fx/wrap_status:Wrap", RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	var checked int
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n.Kind == "call" && n.Evidence != nil {
				checked++
				ev := n.Evidence
				if ev.SymbolID == "" {
					t.Errorf("箭头证据缺 canonical ID: %+v", ev)
				}
				if ev.CallFile == "" || ev.CallLine == 0 {
					t.Errorf("箭头证据缺源码位置: %+v", ev)
				}
				if !strings.HasPrefix(ev.Query, "codeintel query sequence ") || !strings.Contains(ev.Query, "--code") {
					t.Errorf("箭头证据缺可复现查询命令: %+v", ev)
				}
				switch ev.Verdict {
				case SeqVerdictDirect, SeqVerdictCorroborated, SeqVerdictUnverified:
				default:
					t.Errorf("verdict 取值非法: %+v", ev)
				}
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)
	if checked == 0 {
		t.Fatal("夹具应至少有一条带证据的调用箭头")
	}
}
