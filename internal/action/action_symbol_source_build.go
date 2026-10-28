package action

// T13：声明源码契约的构造与路径安全（从 action_symbol_source.go 拆出——
// 行数治理；语义不变）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// buildSymbolSource 用匹配到的声明生成契约（声明全文 + 选中项范围）。
func buildSymbolSource(id, pkg string, d *symbolSourceDecl, fileLabel, location, indexStatus string, src []byte) (*SymbolSource, error) {
	start := d.fset.Position(d.textNode.Pos()).Offset
	end := d.fset.Position(d.textNode.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return nil, fmt.Errorf("声明字节区间越界（文件可能已被改写）: %s [%d,%d)", fileLabel, start, end)
	}
	out := &SymbolSource{
		SymbolID:  id,
		File:      fileLabel,
		LineStart: d.fset.Position(d.textNode.Pos()).Line,
		LineEnd:   d.fset.Position(d.textNode.End()).Line,
		Text:      string(src[start:end]),
		Kind:      d.kind,
		Package:   pkg,
		Location:  location,
		Imports:   d.imports,
	}
	if indexStatus != "" {
		out.IndexStatus = indexStatus
	}
	if d.kind != "function" && d.kind != "method" {
		out.SelectionLineStart = d.fset.Position(d.selNode.Pos()).Line
		out.SelectionLineEnd = d.fset.Position(d.selNode.End()).Line
	}
	return out, nil
}

// parseSymbolSourceID 拆 canonical ID → (import path, 声明名)。
func parseSymbolSourceID(id string) (string, string, error) {
	const prefix = "symbol:go:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", fmt.Errorf("需要 canonical ID（symbol:go:<import_path>:<name>），got %q", id)
	}
	rest := strings.TrimPrefix(id, prefix)
	i := strings.LastIndex(rest, ":")
	if i <= 0 || i == len(rest)-1 {
		return "", "", fmt.Errorf("canonical ID 缺少 <import_path>:<name> 段: %q", id)
	}
	return rest[:i], rest[i+1:], nil
}

// secureSymbolSourceFile 把仓库相对路径解析为绝对路径，并拒绝仓库外目标
// （`..` 穿越、symlink 指向仓库外）。
func secureSymbolSourceFile(repoReal, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("符号没有文件路径（虚拟节点），无法输出源码")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("文件路径必须是仓库相对路径: %q", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("文件路径越出仓库: %q", rel)
	}
	abs := filepath.Join(repoReal, clean)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("源文件不可用（缺失或不可读）: %s: %w", rel, err)
	}
	inside, err := filepath.Rel(repoReal, real)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("源文件在仓库之外（symlink/路径逃逸）: %q", rel)
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("源文件不是普通文件: %s", rel)
	}
	return real, nil
}
