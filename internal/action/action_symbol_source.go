package action

// todo22 §6：`SymbolSource`——按 canonical ID 精确返回函数/方法声明源码。
//
// 契约（CLI：`query symbol <id> --include-source [--json]`）：
//   - 只接受 canonical ID（`symbol:go:<import_path>:<name>`），**不走**
//     ResolveSymbol 的模糊名称搜索——猜一个同名符号等于给出错误源码。
//   - 文件与行号一律来自**当前 AST**（索引里的 LineStart 可能陈旧）。
//   - 声明匹配必须同时命中函数名与接收者类型（`(Store).Insert` 不会取到
//     `(Other).Insert`）；多重声明明确失败。
//   - 包身份用「最近 go.mod 的 module 路径 + 相对目录」校验（支持嵌套
//     module），与 ID 里的 import path 不符即失败。
//   - 拒绝仓库外路径（含 `..` 与 symlink 逃逸）。
//   - 任何失败都返回 error 且不返回部分结果（CLI 非零退出、stdout 无 JSON）。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// SymbolSourceRequest 精确源码请求（RepoAbs = 仓库根绝对路径）。
// T13 兼容入口：SymbolID（原契约）与 Target（新口径）二选一；
// 同时给出且不同 → 明确报错（不静默择一）。
type SymbolSourceRequest struct {
	RepoAbs  string
	SymbolID string
	Target   string
}

// SymbolSourceResult 统一结果类型（T13：SymbolID/Target 两种请求共用同一
// 结果契约；保持 SymbolSource 名字兼容既有调用方）。
type SymbolSourceResult = SymbolSource

// resolveSymbolSourceID 合并 SymbolID/Target 两种请求口径。
func resolveSymbolSourceID(req SymbolSourceRequest) (string, error) {
	id := strings.TrimSpace(req.SymbolID)
	target := strings.TrimSpace(req.Target)
	switch {
	case id == "" && target == "":
		return "", fmt.Errorf("缺少 SymbolID/Target（需要 canonical ID）")
	case id != "" && target != "" && id != target:
		return "", fmt.Errorf("SymbolID (%s) 与 Target (%s) 不一致", id, target)
	case id == "":
		return target, nil
	default:
		return id, nil
	}
}

// SymbolSource 声明源码（JSON 字段顺序即契约顺序）。
type SymbolSource struct {
	SymbolID  string `json:"symbol_id"`
	File      string `json:"file"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Text      string `json:"text"`
	// todo23 §6：声明种类与选中项范围（const/var/type 块保留整组语境时，
	// line_start/end 是整块，selection_* 是所选项）。
	Kind     string `json:"kind,omitempty"`
	Location string `json:"location,omitempty"` // repository|dependency
	// T13：声明所在包（import path；未索引声明也要给出包上下文）。
	Package string `json:"package,omitempty"`
	// todo24 §11.4：声明所在文件的 imports（源文件顺序；无显式别名时省略
	// alias，不按目录末段猜包名）。这是文件上下文，不表示该声明使用了每项。
	Imports            []SymbolSourceImport `json:"imports,omitempty"`
	SelectionLineStart int                  `json:"selection_line_start,omitempty"`
	SelectionLineEnd   int                  `json:"selection_line_end,omitempty"`
	// IndexStatus T13：not_indexed（AST 有声明但图无节点）。作为顶层
	// 元数据输出（CLI 包装），不重复写进 source 明细。
	IndexStatus string `json:"-"`
}

// SymbolSourceImport 源文件的一条导入（显式别名 "."/"_" 原样保留）。
type SymbolSourceImport struct {
	Path  string `json:"path"`
	Alias string `json:"alias,omitempty"`
}

// SymbolSource 按 canonical ID 输出声明源码（todo22 §6；todo23 扩展到
// struct/interface/type/const/var 与依赖源码）。
func (a *Actions) SymbolSource(req SymbolSourceRequest) (*SymbolSource, error) {
	logger := zap.L()
	logger.Info("enter (Actions).SymbolSource", zap.String("symbol_id", req.SymbolID))
	defer logger.Info("exit (Actions).SymbolSource")
	symbolID, err := resolveSymbolSourceID(req)
	if err != nil {
		return nil, err
	}
	req.SymbolID = symbolID
	importPath, name, err := parseSymbolSourceID(req.SymbolID)
	if err != nil {
		return nil, err
	}
	n, nerr := a.repo.GetSymbol(domain.CanonicalID(req.SymbolID))
	indexStatus := ""
	if nerr != nil || n == nil || n.FilePath == "" {
		indexStatus = "not_indexed"
	}
	repoReal, err := filepath.EvalSymlinks(req.RepoAbs)
	if err != nil {
		return nil, fmt.Errorf("仓库根不可用: %s: %w", req.RepoAbs, err)
	}
	if nerr == nil && n.FilePath != "" {
		relFile := filepath.ToSlash(filepath.Clean(n.FilePath))
		absFile, err := secureSymbolSourceFile(repoReal, n.FilePath)
		if err != nil {
			return nil, err
		}
		if err := verifySymbolSourcePackage(importPath, repoReal, absFile); err != nil {
			return nil, err
		}
		src, err := os.ReadFile(absFile)
		if err != nil {
			return nil, fmt.Errorf("读取源文件失败: %s: %w", relFile, err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, absFile, src, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("解析源文件失败（无法确认声明位置）: %s: %w", relFile, err)
		}
		d, err := matchAnySymbolSourceDecl(fset, f, name, relFile)
		if err != nil {
			return nil, err
		}
		return buildSymbolSource(req.SymbolID, importPath, d, relFile, "repository", indexStatus, src)
	}
	// 图无节点/无文件位置：按 import path 离线定位包目录（不联网）。
	return a.symbolSourceFromPackage(req, importPath, name, indexStatus, repoReal,
		nerr == nil && n != nil, nerr == nil && n != nil && n.FilePath == "")
}

// verifySymbolSourcePackage 用「最近 go.mod 的 module 路径 + 相对目录」还原
// 文件的包 import path，与 ID 中的 import path 比对（支持嵌套 module）。
func verifySymbolSourcePackage(importPath, repoReal, absFile string) error {
	modulePath, moduleDir, err := nearestGoMod(filepath.Dir(absFile), repoReal)
	if err != nil {
		return err
	}
	relDir, err := filepath.Rel(moduleDir, filepath.Dir(absFile))
	if err != nil {
		return fmt.Errorf("包身份推导失败: %w", err)
	}
	want := modulePath
	if relDir != "." && relDir != "" {
		want = modulePath + "/" + filepath.ToSlash(relDir)
	}
	if want != importPath {
		return fmt.Errorf("包身份不匹配: ID 声明 %q，最近 go.mod（%s）推导为 %q",
			importPath, modulePath, want)
	}
	return nil
}

// nearestGoMod 从 dir 向上找最近的 go.mod（含 module 声明的路径）。
func nearestGoMod(dir, repoReal string) (modulePath, moduleDir string, err error) {
	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					path := strings.TrimSpace(strings.TrimPrefix(line, "module "))
					path = strings.Trim(path, "\"`")
					if path == "" {
						break
					}
					return path, dir, nil
				}
			}
		}
		if dir == repoReal {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir || !strings.HasPrefix(parent, repoReal) {
			break
		}
		dir = parent
	}
	return "", "", fmt.Errorf("找不到包含该文件的 go.mod（无法校验包身份）: %s", dir)
}

// matchSymbolSourceDecl 在文件里按「函数名 + receiver 类型」精确匹配声明。
// 多重声明、匹配不到都明确失败（不挑第一个）。
func matchSymbolSourceDecl(fset *token.FileSet, f *ast.File, name, relFile string) (*ast.FuncDecl, error) {
	recvType, funcName, isMethod := splitDeclName(name)
	var hits []*ast.FuncDecl
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != funcName {
			continue
		}
		if isMethod {
			if fd.Recv == nil || len(fd.Recv.List) == 0 ||
				recvBaseName(fd.Recv.List[0].Type) != recvType {
				continue
			}
		} else if fd.Recv != nil {
			continue
		}
		hits = append(hits, fd)
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("文件内找不到匹配的声明（函数名/receiver 不匹配）: %s in %s",
			name, relFile)
	case 1:
		return hits[0], nil
	default:
		lines := make([]string, 0, len(hits))
		for _, h := range hits {
			lines = append(lines, fmt.Sprintf("第 %d 行", fset.Position(h.Pos()).Line))
		}
		return nil, fmt.Errorf("文件内有多重声明（%d 处同一函数名/receiver）: %s in %s（%s）",
			len(hits), name, relFile, strings.Join(lines, ", "))
	}
}

// splitDeclName 拆 `(T).method` / `(*T).method` / `Func`。
func splitDeclName(name string) (recvType, funcName string, isMethod bool) {
	if !strings.HasPrefix(name, "(") {
		return "", name, false
	}
	if i := strings.Index(name, ")."); i > 0 {
		recv := strings.TrimPrefix(name[:i], "(")
		recv = strings.TrimPrefix(recv, "*")
		return recv, name[i+2:], true
	}
	return "", name, false
}

// recvBaseName 取接收者类型基名：*T / T / T[P] / T[P, Q] / (T) → T。
func recvBaseName(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.ParenExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t.Name
		case *ast.SelectorExpr:
			return t.Sel.Name
		default:
			return ""
		}
	}
}
