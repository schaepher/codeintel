package action

// U06d：vendor 源码的包身份校验（vendor 边界 + modules.txt + go list 选中文件）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// verifySymbolSourcePackage 用「最近 go.mod 的 module 路径 + 相对目录」还原
// 文件的包 import path，与 ID 中的 import path 比对（支持嵌套 module）。
func verifySymbolSourcePackage(importPath, repoReal, absFile string) error {
	// U06d：vendor 目录下的源码按 **vendor 边界 + modules.txt + go list** 校验，
	// 不能用"主 module 路径 + vendor 相对目录"推导（那是 example.com/app/
	// vendor/example.com/lib，与真实 import path 不同）。
	if moduleRoot, ok := seqVendorModuleRoot(absFile); ok {
		return verifyVendorSourcePackage(importPath, moduleRoot, absFile)
	}
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

// seqVendorModuleRoot 最近的 `<module>/vendor/<pkg>` 边界（父目录有 go.mod）。
func seqVendorModuleRoot(absFile string) (string, bool) {
	dir := filepath.Dir(absFile)
	for {
		if filepath.Base(dir) == "vendor" {
			parent := filepath.Dir(dir)
			if _, err := os.Stat(filepath.Join(parent, "go.mod")); err == nil {
				return parent, true
			}
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// verifyVendorSourcePackage vendor 依赖的包身份：目录路径 == import path、
// modules.txt 登记该包、go list -mod=vendor 选中该目录且文件在选中集合内。
func verifyVendorSourcePackage(importPath, moduleRoot, absFile string) error {
	vendorDir := filepath.Join(moduleRoot, "vendor")
	relDir, err := filepath.Rel(vendorDir, filepath.Dir(absFile))
	if err != nil {
		return fmt.Errorf("vendor 包身份推导失败: %w", err)
	}
	want := filepath.ToSlash(relDir)
	if want != importPath {
		return fmt.Errorf("包身份不匹配: ID 声明 %q，vendor 目录推导为 %q（须与 canonical import path 相同）",
			importPath, want)
	}
	// modules.txt 必须登记该包（vendor 目录存在不代表该依赖被选中）。
	modTxt, err := os.ReadFile(filepath.Join(vendorDir, "modules.txt"))
	if err != nil {
		return fmt.Errorf("vendor 清单缺失（vendor/modules.txt）: %w", err)
	}
	if !vendorModulesContains(string(modTxt), importPath) {
		return fmt.Errorf("vendor/modules.txt 未登记包 %q（不能仅凭目录存在给出源码）", importPath)
	}
	// 离线 go list -mod=vendor 核对目录与选中文件（20 秒上限）。
	dir, files, err := seqGoListVendorPkg(moduleRoot, importPath)
	if err != nil {
		return fmt.Errorf("vendor 包选择失败: %w", err)
	}
	realDir, err1 := filepath.EvalSymlinks(dir)
	wantDir, err2 := filepath.EvalSymlinks(filepath.Dir(absFile))
	if err1 != nil || err2 != nil || filepath.Clean(realDir) != filepath.Clean(wantDir) {
		return fmt.Errorf("vendor 包目录不符: go list 选中 %q，文件位于 %q", dir, filepath.Dir(absFile))
	}
	base := filepath.Base(absFile)
	for _, f := range files {
		if f == base {
			return nil
		}
	}
	return fmt.Errorf("文件 %q 不在 go list 选中的 vendor 文件集（可能被构建标签排除）", base)
}

// vendorModulesContains modules.txt 是否登记该包（含 "# <module>" 清单与包行）。
func vendorModulesContains(text, importPath string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == importPath || strings.HasPrefix(line, importPath+" ") {
			return true
		}
	}
	return false
}
