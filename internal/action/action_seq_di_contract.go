package action

// T06：DI 容器的**字段注入代码契约**证明。
//
// 反射式容器（`RegisterBeanByType(key, reflect.TypeOf(...))` + `GetBean` +
// `FieldByName` + `Set`）的注册证据只能证明"key 注册了类型"，不能证明字段
// 真的被写入。这里要求容器源码里存在一个函数同时满足：
//   - 读取注解参数（`.Value()` / `GetStructByAnno(...)`），
//   - 取值（`GetBean(...)` / `Get(...)`），
//   - 反射字段定位（`FieldByName(...)`），
//   - 赋值（`val.Set(v)`）。
// 四步同函数 = 注入机制成立；否则保持未知（不能只因类型相容/唯一实现升级）。
//
// 只扫**本仓 module 内**源码（离线、请求内缓存），不联网/不跨仓猜。

import (
	"go/ast"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqDIContract 注入机制证据（file:line + 命中的步骤）。
type seqDIContract struct {
	file  string
	line  int
	steps []string
}

// seqDIContainerContract 找容器自身的字段注入实现（请求内缓存）。
func seqDIContainerContract(a *Actions, req CodeSequenceRequest) (seqDIContract, bool) {
	if req.diContract != nil {
		if v, ok := req.diContract[""]; ok {
			return v.contract, v.ok
		}
	}
	res := seqDIContractCacheEntry{}
	// 1) 当前包（同目录）优先：容器就在同包的常见形态。
	for _, rel := range seqPackageFiles(req) {
		if c, ok := seqDIContractInFile(req, rel); ok {
			res = seqDIContractCacheEntry{contract: c, ok: true}
			break
		}
	}
	// 2) 跨包容器（di/di.go）：按索引里的函数节点定点扫描文件（每文件一次
	//    解析，请求内缓存）；只在同包没找到时触发，避免常态开销。
	if !res.ok {
		res = seqDIContractFromRepo(a, req)
	}
	if req.diContract != nil {
		req.diContract[""] = res
	}
	return res.contract, res.ok
}

func seqDIContractInFile(req CodeSequenceRequest, rel string) (seqDIContract, bool) {
	ctx := seqFileCtxFor(req, rel)
	if ctx == nil || ctx.file == nil {
		return seqDIContract{}, false
	}
	for _, decl := range ctx.file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		steps := map[string]bool{}
		line := 0
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			switch name {
			case "GetBean", "Get", "Resolve", "Lookup":
				steps["get"] = true
			case "FieldByName":
				steps["field"] = true
			case "Set":
				steps["set"] = true
			case "Value", "GetStructByAnno", "GetAnnotation":
				steps["annotation"] = true
			}
			if len(steps) == 4 && line == 0 {
				line = ctx.fset.Position(call.Lparen).Line
			}
			return true
		})
		if len(steps) != 4 {
			continue
		}
		keys := make([]string, 0, 4)
		for _, k := range []string{"annotation", "get", "field", "set"} {
			if steps[k] {
				keys = append(keys, k)
			}
		}
		return seqDIContract{file: rel, line: line, steps: keys}, true
	}
	return seqDIContract{}, false
}

// seqDIContractFromRepo 按索引函数节点扫描仓库内文件找注入实现（去重文件、
// 限规模；只用于反射容器注册的证明，常态零成本）。
func seqDIContractFromRepo(a *Actions, req CodeSequenceRequest) seqDIContractCacheEntry {
	const maxFiles = 400
	fns, err := a.repo.GetFunctions()
	if err != nil || len(fns) == 0 {
		return seqDIContractCacheEntry{}
	}
	// GetFunctions 是轻量投影（无 file_path）——按 ID 批量取回位置（一次查询）。
	ids := make([]domain.CanonicalID, 0, len(fns))
	for _, fn := range fns {
		if fn != nil && fn.ID != "" {
			ids = append(ids, fn.ID)
		}
	}
	nodes, err := a.repo.GetSymbolsByIDs(ids)
	if err != nil {
		return seqDIContractCacheEntry{}
	}
	seen := map[string]bool{}
	scanned := 0
	for _, fn := range nodes {
		if fn == nil || fn.FilePath == "" || seen[fn.FilePath] {
			continue
		}
		seen[fn.FilePath] = true
		scanned++
		if scanned > maxFiles {
			break
		}
		if c, ok := seqDIContractInFile(req, fn.FilePath); ok {
			return seqDIContractCacheEntry{contract: c, ok: true}
		}
	}
	return seqDIContractCacheEntry{}
}

// seqDIInjectEvidence 容器契约证据的展示文本（file:line + 步骤）。
func seqDIInjectEvidence(c seqDIContract) string {
	if c.file == "" {
		return ""
	}
	return filepath.ToSlash(c.file) + ":" + itoaSeq(c.line) + " [" + strings.Join(c.steps, "+") + "]"
}

// seqDIContractCacheEntry 请求内缓存条目（含"未命中"）。
type seqDIContractCacheEntry struct {
	contract seqDIContract
	ok       bool
}
