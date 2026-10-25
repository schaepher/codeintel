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

// fixtureFields 一个函数体里的字段读写（fixture 级近似，无类型信息）。
type fixtureFields struct {
	writes map[string]int // 字段名 → 首次写入行
	reads  map[string]int // 字段名 → 首次读取行
}

func (f fixtureFields) lineOf(name string) int {
	if n, ok := f.writes[name]; ok {
		return n
	}
	return f.reads[name]
}

// fixtureFieldAccess 收集 selector 形态的字段写入（x.F = …）与读取
// （出现在条件/实参里的 x.F）。
func fixtureFieldAccess(body *ast.BlockStmt) fixtureFields {
	out := fixtureFields{writes: map[string]int{}, reads: map[string]int{}}
	if body == nil {
		return out
	}
	fset := token.NewFileSet()
	ast.Inspect(body, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range as.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok {
					if _, seen := out.writes[sel.Sel.Name]; !seen {
						out.writes[sel.Sel.Name] = fset.Position(sel.Pos()).Line
					}
				}
			}
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// 读取：作为条件/实参出现（写入已在上面跳过——这里不区分，
		// 两者都记：因果判据要的是"读过这个字段"）
		if _, seen := out.reads[sel.Sel.Name]; !seen {
			out.reads[sel.Sel.Name] = fset.Position(sel.Pos()).Line
		}
		return true
	})
	return out
}
