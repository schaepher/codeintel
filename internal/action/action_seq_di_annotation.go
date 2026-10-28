package action

// todo22 §3.3：字段注入 key 的注解解析（注释形式优先，struct tag 作为
// 补充；不假设只存在 tag）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// diFieldKey 字段 key：注解（doc/spec 注释）> struct tag > 字段名。
func diFieldKey(fset *token.FileSet, f *ast.File, recvType, field string) (string, string) {
	var (
		key   string
		found bool
		from  string
	)
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != recvType {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, fl := range st.Fields.List {
			// 支持一行多名字段
			hit := false
			for _, nm := range fl.Names {
				if nm.Name == field {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			if k, ok := diAnnotationKey(fset, f, fl); ok {
				key, found, from = k, true, seqReasonDIAnnotation
			}
			return false
		}
		return true
	})
	if !found {
		return "", ""
	}
	return key, from
}

// diAnnotationKey 从注释（doc/行内）或 struct tag 提取注入 key。
func diAnnotationKey(fset *token.FileSet, f *ast.File, fl *ast.Field) (string, bool) {
	// 注释形式：`// @inject store` / `//di:store` / `// inject:store`
	for _, group := range []*ast.CommentGroup{fl.Doc, fl.Comment} {
		if group == nil {
			continue
		}
		for _, c := range group.List {
			if k, ok := diAnnotationInText(c.Text); ok {
				return k, true
			}
		}
	}
	// T06：同一行的块注释（`/*@Inject(name=worker)*/ Worker ...`）不会被 Go
	// parser 挂到 Field.Doc——按位置找"字段前面最近且同行/上一行"的注释。
	if k, ok := diAnnotationNearField(fset, f, fl); ok {
		return k, true
	}
	// struct tag 形式（不假设只有 tag——注释优先）
	if fl.Tag != nil {
		tag := strings.Trim(fl.Tag.Value, "`")
		for _, name := range []string{"inject", "di"} {
			if k, ok := tagValue(tag, name); ok {
				return k, true
			}
		}
	}
	return "", false
}

// diAnnotationNearField 同一行/紧邻上一行的块注释注解（parser 不挂 Doc 的
// 形态）；只接受"最近的一条"且必须含注入注解。
func diAnnotationNearField(fset *token.FileSet, f *ast.File, fl *ast.Field) (string, bool) {
	if fset == nil || f == nil || fl == nil {
		return "", false
	}
	fieldLine := fset.Position(fl.Pos()).Line
	var best *ast.CommentGroup
	bestEnd := token.NoPos
	for _, cg := range f.Comments {
		if cg.End() > fl.Pos() {
			continue
		}
		endLine := fset.Position(cg.End()).Line
		if endLine != fieldLine && endLine != fieldLine-1 {
			continue
		}
		if cg.End() > bestEnd {
			best, bestEnd = cg, cg.End()
		}
	}
	if best == nil {
		return "", false
	}
	for _, c := range best.List {
		if k, ok := diAnnotationInText(c.Text); ok {
			return k, true
		}
	}
	return "", false
}

// diAnnotationParam 注解括号参数 → key：`name=worker` / `value=worker` /
// `worker` / `"worker"`。
func diAnnotationParam(inner string) (string, bool) {
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return "", false
	}
	for _, kv := range strings.Split(inner, ",") {
		kv = strings.TrimSpace(kv)
		if i := strings.Index(kv, "="); i > 0 {
			k := strings.TrimSpace(kv[:i])
			v := strings.Trim(strings.TrimSpace(kv[i+1:]), "\"'")
			if (k == "name" || k == "value" || k == "key") && v != "" {
				return v, true
			}
			continue
		}
		v := strings.Trim(kv, "\"'")
		if v != "" && !strings.ContainsAny(v, " =") {
			return v, true
		}
	}
	return "", false
}

// diAnnotationInText 解析注释文本里的注入注解（key 非空）。
func diAnnotationInText(text string) (string, bool) {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "//"))
	t = strings.TrimSpace(strings.TrimPrefix(t, "/*"))
	t = strings.TrimSpace(strings.TrimSuffix(t, "*/"))
	low := strings.ToLower(t)
	for _, prefix := range []string{"@inject", "inject:", "inject ", "@di", "di:", "di "} {
		if strings.HasPrefix(low, prefix) {
			k := strings.TrimSpace(t[len(prefix):])
			// T06：注解参数形式 `@Inject(name=worker)` / `@Inject("worker")`
			// / `@Inject(worker)`——从括号里取 key，不留括号字符。
			if strings.HasPrefix(k, "(") && strings.HasSuffix(k, ")") {
				inner := strings.TrimSpace(k[1 : len(k)-1])
				if v, ok := diAnnotationParam(inner); ok {
					return v, true
				}
				k = inner
			}
			k = strings.Trim(k, "\"'")
			if k != "" && !strings.ContainsAny(k, " \t") {
				return k, true
			}
		}
	}
	return "", false
}

// tagValue 从 struct tag 文本取 name:"value" 的值。
func tagValue(tag, name string) (string, bool) {
	i := strings.Index(tag, name+":\"")
	if i < 0 {
		return "", false
	}
	rest := tag[i+len(name)+2:]
	j := strings.Index(rest, "\"")
	if j <= 0 {
		return "", false
	}
	return rest[:j], true
}

// diFieldKeyInPackage 字段注解 key：当前文件优先；找不到时沿**嵌入字段**
// 在包内查找（Box 嵌入 Base，Base.Store 的注解同样适用；多条提升路径
// 给出不同 key 时保守返回未知）。
func diFieldKeyInPackage(req CodeSequenceRequest, f *ast.File, recvType, field string) (string, string) {
	var fset *token.FileSet
	if req.curCtx != nil {
		fset = req.curCtx.fset
	}
	if k, from := diFieldKey(fset, f, recvType, field); from != "" {
		return k, from
	}
	rel := seqFileRelOf(req, f)
	var keys []string
	var reasons []string
	seen := map[string]bool{}
	var walk func(typeName string, depth int)
	walk = func(typeName string, depth int) {
		if typeName == "" || depth > 4 || seen[typeName] {
			return
		}
		seen[typeName] = true
		ctx, st, ok := seqFindStructInPackage(req, rel, typeName)
		if !ok {
			return
		}
		for _, emb := range seqEmbeddedTypeNames(ctx, st) {
			if k, from := diFieldKey(ctx.fset, ctx.file, emb, field); from != "" {
				keys = append(keys, k)
				reasons = append(reasons, from)
				continue
			}
			walk(emb, depth+1)
		}
	}
	walk(recvType, 0)
	if len(keys) == 0 {
		return "", ""
	}
	for _, k := range keys[1:] {
		if k != keys[0] {
			return "", ""
		}
	}
	return keys[0], reasons[0]
}
