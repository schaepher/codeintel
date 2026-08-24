package ast

// R35 urfave/cli/v2 命令树识别（待办 3——不依赖文件路径，从代码事实
// 发现）：`cli.App{Commands: []*cli.Command{{Name, Usage, Action}}}` 复合
// 字面量 + 包级 `var Commands = []*cli.Command{...}` 变量（v2 主流两种
// 形态），递归一层 Subcommands。每命令发射 cli_command 节点（名称/用法/
// action/父命令/位置）——查询命令 query cli-routes + wiki commands 页
// 消费。

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// cliCommandDef 一个命令定义（构建期提取，含嵌套）。
type cliCommandDef struct {
	Name        string
	Usage       string
	Action      string
	Subcommands []cliCommandDef
	Pos         token.Pos
}

// isCLIApp 类型是 *cli.App 或 cli.App（urfave/cli/v2）。
func isCLIApp(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "github.com/urfave/cli/v2" &&
		n.Obj().Name() == "App"
}

// isCLICommandSlice 类型是 []*cli.Command（包级 Commands 变量）。
func isCLICommandSlice(t types.Type) bool {
	s, ok := t.(*types.Slice)
	if !ok {
		return false
	}
	p, ok := s.Elem().(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := p.Elem().(*types.Named)
	if !ok {
		return false
	}
	return n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "github.com/urfave/cli/v2" &&
		n.Obj().Name() == "Command"
}

// cliCommandList 从 []*cli.Command 复合字面量提取命令清单。
func cliCommandList(pkg *packages.Package, lit *ast.CompositeLit) []cliCommandDef {
	var out []cliCommandDef
	for _, el := range lit.Elts {
		cl, ok := el.(*ast.CompositeLit)
		if !ok {
			continue
		}
		out = append(out, cliCommandFromLit(pkg, cl))
	}
	return out
}

// cliCommandFromLit 单个 *cli.Command 复合字面量：Name/Usage/Action/
// Subcommands 字段。
func cliCommandFromLit(pkg *packages.Package, lit *ast.CompositeLit) cliCommandDef {
	var def cliCommandDef
	def.Pos = lit.Pos()
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			def.Name = stringLit(kv.Value)
		case "Usage":
			def.Usage = stringLit(kv.Value)
		case "Action":
			def.Action = actionName(kv.Value)
		case "Subcommands":
			if sub, ok := kv.Value.(*ast.CompositeLit); ok {
				def.Subcommands = cliCommandList(pkg, sub)
			}
		}
	}
	return def
}

// stringLit 字符串字面量值。
func stringLit(e ast.Expr) string {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return ""
	}
	return s
}

// actionName Action 字段名：函数引用 Ident 取函数名；FuncLit 取
// "(匿名)"（位置在 register 属性）。
func actionName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	if _, ok := e.(*ast.FuncLit); ok {
		return "(匿名)"
	}
	return ""
}

// markCLICommands 遍历包识别命令树并发射 cli_command 节点。挂
// processPackage（与 markGrpcServiceInterfaces 同层）。
func (a *Adapter) markCLICommands(repo *domain.Repository, pkg *packages.Package, emit domain.EmitFunc) error {
	for _, f := range pkg.Syntax {
		// 包级 var Commands = []*cli.Command{...}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "Commands" || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.CompositeLit)
				if !ok || !isCLICommandSlice(pkg.TypesInfo.TypeOf(vs.Values[0])) {
					continue
				}
				for _, c := range cliCommandList(pkg, lit) {
					emitCLICommandNode(repo, pkg, emit, c, "")
				}
			}
		}
		// cli.App{Commands: ...} 复合字面量
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			t := pkg.TypesInfo.TypeOf(lit)
			if t == nil || !isCLIApp(t) {
				return true
			}
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "Commands" {
					continue
				}
				cl, ok := kv.Value.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, c := range cliCommandList(pkg, cl) {
					emitCLICommandNode(repo, pkg, emit, c, "")
				}
			}
			return true
		})
	}
	return nil
}

// emitCLICommandNode 发射命令节点（嵌套递归——parent 拼接名称）。
func emitCLICommandNode(repo *domain.Repository, pkg *packages.Package, emit domain.EmitFunc, def cliCommandDef, parent string) {
	if def.Name == "" {
		return
	}
	full := def.Name
	if parent != "" {
		full = parent + "." + def.Name
	}
	pos := pkg.Fset.PositionFor(def.Pos, false)
	_ = emit(domain.Item{Node: &domain.CodeEntity{
		ID:   domain.CanonicalID("symbol:go:" + pkg.PkgPath + ":cmd." + full),
		Kind: domain.KindCLICommand,
		Name: full,
		Properties: map[string]any{
			"cli_name":   def.Name,
			"cli_usage":  def.Usage,
			"cli_action": def.Action,
			"cli_parent": parent,
			"register":   fmt.Sprintf("%s:%d", relPath(repo.Path, pos.Filename), pos.Line),
		},
	}})
	for _, sub := range def.Subcommands {
		emitCLICommandNode(repo, pkg, emit, sub, full)
	}
}
