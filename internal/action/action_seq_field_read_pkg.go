package action

// U04v：字段读取的包/文件定位（仓库内模块 + 外部依赖）。

import (
	"os"
	"path/filepath"
	"strings"
)

// seqPkgFilesForReq 按 import path 取包文件（仓库内模块 + 外部依赖）。
func seqPkgFilesForReq(req CodeSequenceRequest, pkgPath, relFile string) []*seqFileCtx {
	if pkgPath == "" {
		return nil
	}
	if dir, ok := seqRepoPkgDir(req, pkgPath); ok {
		var out []*seqFileCtx
		entries, err := os.ReadDir(filepath.Join(req.RepoAbs, dir))
		if err == nil {
			for _, ent := range entries {
				name := ent.Name()
				if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				rel := name
				if dir != "" {
					rel = dir + "/" + name
				}
				if ctx := seqFileCtxFor(req, rel); ctx != nil {
					out = append(out, ctx)
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if req.acts != nil {
		if pkg := seqExternalPackage(req.acts, req, pkgPath); pkg != nil && pkg.err == nil {
			return pkg.files
		}
	}
	return nil
}

// seqRepoPkgDir import path → 仓库内相对目录（非仓库内包返回 false）。
func seqRepoPkgDir(req CodeSequenceRequest, pkgPath string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(req.RepoAbs, "go.mod"))
	if err != nil {
		return "", false
	}
	module := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			module = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module ")), "\"`")
			break
		}
	}
	if module == "" {
		return "", false
	}
	switch {
	case pkgPath == module:
		return "", true
	case strings.HasPrefix(pkgPath, module+"/"):
		return filepath.ToSlash(strings.TrimPrefix(pkgPath, module+"/")), true
	}
	return "", false
}

// seqBuiltinTypeName 预声明标量类型（字段读取的标量类型不需要包身份）。
func seqBuiltinTypeName(name string) bool {
	switch name {
	case "string", "bool", "byte", "rune", "error", "any",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "complex64", "complex128":
		return true
	}
	return false
}
