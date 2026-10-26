package action

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// focusFieldHit 字段路径是否命中任一焦点目标（后缀 + 点边界匹配，大小写
// 不敏感——Go 字段名与列名常大小写不同）。
func focusFieldHit(fieldPath string, targets []string) bool {
	fp := strings.ToLower(fieldPath)
	for _, t := range targets {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if fp == t || strings.HasSuffix(fp, "."+t) {
			return true
		}
	}
	return false
}

// seqResultSlot 返回表达式 → 结果槽名：具名结果按位对应，否则 #序号
// （多返回值且表达式个数与签名不一致时也只给序号——不做猜测）。
func seqResultSlot(i, exprs int, names []string) string {
	if exprs == len(names) && i < len(names) && names[i] != "" {
		return names[i]
	}
	return "#" + itoaSeq(i)
}

// itoaSeq 小整数转字符串（避免再引 strconv 依赖）。
func itoaSeq(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

// filterFieldLeaf 字段叶子名匹配（区分大小写——Go 字段名）。
func filterFieldLeaf(paths []string, leaf string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, "."+leaf) {
			out = append(out, p)
		}
	}
	return out
}

// focusLeafHit AST 字段文本是否命中解析后的字段身份（按叶子名）。
func focusLeafHit(leaf string, ids []string) bool {
	for _, id := range ids {
		if i := strings.LastIndex(id, "."); i >= 0 {
			id = id[i+1:]
		}
		if id == leaf {
			return true
		}
	}
	return false
}

// validateFocusTargets 焦点目标存在性/歧义校验（todo12-9 R3）：
//   - "return"/"result" → 合法
//   - 局部变量/形参名（入口函数内）→ 合法（可能无写入，正常返回）
//   - 字段名/类型限定字段 → 必须存在且唯一解析为类型身份（歧义报错）
func (a *Actions) validateFocusTargets(n *domain.CodeEntity, f *seqFocus) error {
	if f == nil || len(f.fields) == 0 {
		return nil
	}
	paths, err := a.repo.GetAllFieldPaths()
	if err != nil {
		paths = nil
	}
	locals := a.entryLocalNames(n)
	var resolved []string
	for _, raw := range f.fields {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if strings.Contains(t, "/") || strings.Count(t, ".") >= 1 {
			ids, rerr := a.resolveFocusFieldPath(t, paths)
			if rerr != nil {
				return rerr
			}
			resolved = append(resolved, ids...)
			continue
		}

		if locals[t] {
			continue
		}
		matches := uniqueStrings(filterFieldLeaf(paths, t))
		if len(matches) == 0 {

			if id, ok := a.entryStructFieldIdentity(n, t); ok {
				resolved = append(resolved, id)
				continue
			}
		}
		switch len(matches) {
		case 0:
			return fmt.Errorf("焦点目标 %q 不存在（不是当前函数的局部变量，也不是索引中的字段）", raw)
		case 1:
			resolved = append(resolved, matches[0])
		default:
			return fmt.Errorf("焦点目标 %q 有 %d 个字段身份，存在歧义，请用类型限定路径：%s",
				raw, len(matches), strings.Join(matches, " / "))
		}
	}
	f.fieldIDs = uniqueStrings(resolved)
	for _, id := range f.fieldIDs {
		if ws, werr := a.repo.GetFieldWriters(id); werr == nil {
			for _, w := range ws {
				f.writers[w] = true
			}
		}
	}
	return nil
}

// entryStructFieldIdentity 入口 receiver 结构体的字段身份（无写入的合法字段）。
func (a *Actions) entryStructFieldIdentity(n *domain.CodeEntity, field string) (string, bool) {
	if n.FilePath == "" {
		return "", false
	}
	src, err := os.ReadFile(filepath.Join(a.repo.RepoPath(), n.FilePath))
	if err != nil {
		return "", false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return "", false
	}
	_, decl := findFuncDecl(fset, f, n.LineStart)
	if decl == nil || decl.Recv == nil || len(decl.Recv.List) == 0 {
		return "", false
	}
	recv := starTypeName(decl.Recv.List[0].Type)
	if recv == "" {
		return "", false
	}
	node, err := a.resolveStructNode(pkgPathOf(string(n.ID)) + "." + recv)
	if err != nil {
		return "", false
	}
	exists, verifiable := structHasField(node, field)
	if !verifiable || !exists {
		return "", false
	}
	return typeIdentity(node) + "." + field, true
}

// resolveFocusFieldPath 类型限定字段引用 → 唯一字段身份；摘要里没有该
// 字段时回退到结构体字段元数据（合法但无写入的字段），都没有 → 报错。
func (a *Actions) resolveFocusFieldPath(ref string, paths []string) ([]string, error) {
	ids, err := resolveFocusFieldPaths(ref, paths)
	if err == nil {
		return ids, nil
	}
	i := strings.LastIndex(ref, ".")
	if i > 0 {
		typeRef, field := ref[:i], ref[i+1:]
		if node, nerr := a.resolveStructNode(typeRef); nerr == nil {
			if exists, verifiable := structHasField(node, field); verifiable && exists {
				return []string{typeIdentity(node) + "." + field}, nil
			}
		}
	}
	return nil, err
}

// resolveFocusFieldPaths 类型限定字段引用 → 唯一字段身份（不存在/歧义报错）。
func resolveFocusFieldPaths(ref string, paths []string) ([]string, error) {
	i := strings.LastIndex(ref, ".")
	if i <= 0 {
		return nil, fmt.Errorf("焦点目标 %q 不是合法字段路径", ref)
	}
	typeRef, field := ref[:i], ref[i+1:]
	var want string
	if strings.Contains(typeRef, "/") {
		j := strings.LastIndex(typeRef, ".")
		if j < 0 {
			return nil, fmt.Errorf("焦点目标 %q 缺少类型名", ref)
		}
		want = typeRef[:j] + "." + typeRef[j+1:] + "." + field
	} else {
		name := typeRef
		if j := strings.LastIndex(typeRef, "."); j >= 0 {
			name = typeRef[j+1:]
		}
		for _, p := range paths {
			if strings.HasSuffix(p, "."+name+"."+field) {
				if want != "" && want != p {
					return nil, fmt.Errorf("焦点目标 %q 有多个类型身份，存在歧义", ref)
				}
				want = p
			}
		}
		if want == "" {
			return nil, fmt.Errorf("焦点目标 %q 不存在（未找到类型 %s 的字段 %s）", ref, name, field)
		}
		return []string{want}, nil
	}
	for _, p := range paths {
		if p == want {
			return []string{want}, nil
		}
	}
	return nil, fmt.Errorf("焦点目标 %q 不存在（索引中无字段 %s）", ref, want)
}

// entryLocalNames 入口函数的局部变量/形参名（焦点合法性判定）。
func (a *Actions) entryLocalNames(n *domain.CodeEntity) map[string]bool {
	out := map[string]bool{}
	if n.FilePath == "" {
		return out
	}
	abs := a.repo.RepoPath()
	src, err := os.ReadFile(filepath.Join(abs, n.FilePath))
	if err != nil {
		return out
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return out
	}
	_, decl := findFuncDecl(fset, f, n.LineStart)
	if decl == nil {
		return out
	}
	for _, fl := range []*ast.FieldList{decl.Recv, decl.Type.Params, decl.Type.Results} {
		if fl == nil {
			continue
		}
		for _, fd := range fl.List {
			for _, name := range fd.Names {
				out[name.Name] = true
			}
		}
	}
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					out[id.Name] = true
				}
			}
		case *ast.DeclStmt:
			if gd, ok := x.Decl.(*ast.GenDecl); ok {
				for _, sp := range gd.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							out[name.Name] = true
						}
					}
				}
			}
		}
		return true
	})
	return out
}
