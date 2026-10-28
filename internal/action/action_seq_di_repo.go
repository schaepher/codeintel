package action

// todo22 P1：DI 注册的**跨包定位**与框架式 Bean 注解契约。
//
// 边界：
//   - 跨包注册只解析索引里已知的引导函数（init/Init/Setup/RegisterDeps/
//     Bootstrap）所在文件——不遍历全仓源码；只在当前包没有该 key 的注册时
//     才触发（常见路径零成本）。
//   - Bean/Provider 注解（`// @bean store`、`// di:bean store`、
//     `// provide: store`）把 provider 的**第一个返回类型**登记为 key 的
//     实现；字段密钥必须来自显式注解（扩展契约不看字段名兜底）。
//   - 所有结果都带注册/注解的 file:line，可核对。

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
)

// seqDIRegScannedKey 请求级缓存哨兵：区分"还没扫描"与"扫描后为空"。
const seqDIRegScannedKey = "\x00scanned"

// seqBootstrapFuncNames 索引里可能承载注册调用的引导函数名。
var seqBootstrapFuncNames = []string{"init", "Init", "Setup", "RegisterDeps", "Bootstrap"}

// seqDIRegistrationsRepo 当前包 + 跨包引导函数里的注册证据（请求内缓存）。
func seqDIRegistrationsRepo(a *Actions, req CodeSequenceRequest) map[string][]seqDIReg {
	// 请求级缓存（map 按引用共享；空 map 不能当命中标志，用哨兵键）
	if req.diRegsAll != nil {
		if out, ok := req.diRegsAll[seqDIRegScannedKey]; ok {
			return out
		}
	}
	out := map[string][]seqDIReg{}
	for k, list := range seqDIRegistrations(a, req) {
		out[k] = append(out[k], list...)
	}
	fns, err := a.repo.GetFunctionsByNames(seqBootstrapFuncNames, 500)
	if err == nil {
		curDir := req.curFile
		if i := lastSlash(curDir); i >= 0 {
			curDir = curDir[:i]
		} else {
			curDir = ""
		}
		seen := map[string]bool{}
		for _, fn := range fns {
			if fn == nil || fn.FilePath == "" || fn.ID == "" {
				continue
			}
			dir := fn.FilePath
			if i := lastSlash(dir); i >= 0 {
				dir = dir[:i]
			} else {
				dir = ""
			}
			if dir == curDir {
				continue // 当前包已扫过
			}
			id := string(fn.ID)
			if seen[id] {
				continue
			}
			seen[id] = true
			sub := req
			sub.curSym = id
			sub.curFile = fn.FilePath
			ctx, fd, ok := loadSeqFuncDecl(a, req, id)
			if !ok || fd == nil || fd.Body == nil {
				continue
			}
			seqScanRegistrations(a, sub, ctx, fd.Body, out)
			seqScanBeans(sub, ctx, out)
		}
	}
	if req.diRegsAll != nil {
		req.diRegsAll[seqDIRegScannedKey] = out
	}
	return out
}

// seqScanBeans 扫描一个文件里的 Bean/Provider 注解登记。
func seqScanBeans(req CodeSequenceRequest, ctx *seqFileCtx, out map[string][]seqDIReg) {
	if ctx == nil || ctx.file == nil {
		return
	}
	for _, decl := range ctx.file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Recv != nil {
			continue
		}
		key, ok := seqBeanAnnotation(fd.Doc)
		if !ok {
			continue
		}
		if fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
			continue
		}
		ref, ok := seqTypeRefOf(req, ctx, fd.Type.Results.List[0].Type)
		if !ok {
			continue
		}
		if key == "" {
			key = ref.typeName
		}
		line := 0
		if ctx.fset != nil {
			line = ctx.fset.Position(fd.Pos()).Line
		}
		dup := false
		for _, r := range out[key] {
			if r.file == ctx.rel && r.line == line && r.typeName == ref.typeName {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out[key] = append(out[key], seqDIReg{key: key, typeName: ref.typeName,
			pkgPath: ref.pkgPath, file: ctx.rel, line: line, kind: "bean"})
	}
}

// seqBeanAnnotation 声明注释里的 Bean/Provider 注解：返回显式 key（可为空，
// 空表示用返回类型名）。识别 `@bean` / `@Bean` / `di:bean` / `provide:`。
func seqBeanAnnotation(doc *ast.CommentGroup) (string, bool) {
	if doc == nil {
		return "", false
	}
	for _, c := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		if text == "" {
			continue
		}
		lower := strings.ToLower(text)
		for _, tok := range []string{"@bean", "di:bean", "provide:"} {
			i := strings.Index(lower, tok)
			if i < 0 {
				continue
			}
			rest := strings.TrimSpace(text[i+len(tok):])
			if strings.HasPrefix(rest, "(") {
				rest = strings.TrimPrefix(rest, "(")
				if j := strings.Index(rest, ")"); j >= 0 {
					rest = rest[:j]
				}
			} else if j := strings.IndexAny(rest, " \t,"); j >= 0 {
				rest = rest[:j]
			}
			rest = strings.Trim(rest, "\"'` ,")
			return rest, true
		}
	}
	return "", false
}

// seqPackageFiles 当前包（同目录）的 .go 文件列表（仓库相对路径）。
func seqPackageFiles(req CodeSequenceRequest) []string {
	return seqPackageFilesOf(req, req.curFile)
}

// seqPackageFilesOf 指定文件所在包（同目录）的 .go 文件列表。
func seqPackageFilesOf(req CodeSequenceRequest, relFile string) []string {
	dir := relFile
	if i := lastSlash(dir); i >= 0 {
		dir = dir[:i]
	} else {
		dir = ""
	}
	var out []string
	entries, err := os.ReadDir(filepath.Join(req.RepoAbs, dir))
	if err != nil {
		return nil
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if dir == "" {
			out = append(out, name)
		} else {
			out = append(out, dir+"/"+name)
		}
	}
	return out
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
