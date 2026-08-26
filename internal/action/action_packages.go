package action

// R9x 迁移：`query packages` 聚合逻辑（原 cli/query_packages.go 的
// packagesData + cli/wiki_packages.go 的 packageDoc）——包路径 +
// doc_comment（去 Copyright）+ 无说明时包内代码事实（结构体/方法/
// 函数签名）。cli 只做参数转发与输出；wiki/MCP 经 Actions.PackagesData
// 同源调用。

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// PkgInfo 一个包的结构信息（输出契约）。
type PkgInfo struct {
	Path    string   `json:"path"`    // 包路径（module 内）
	Name    string   `json:"name"`    // 短名（路径末段）
	Doc     string   `json:"doc"`     // 包 doc_comment（去 Copyright；空 = 无包级说明）
	Structs []string `json:"structs"` // 无 doc 时：包内结构体（字段数）
	Methods []string `json:"methods"` // 无 doc 时：方法签名（截断）
	Funcs   []string `json:"funcs"`   // 无 doc 时：函数签名（截断）
}

// PackagesData 收集全部包的结构信息（R77：wiki 包结构节同款数据）。
func (a *Actions) PackagesData() ([]PkgInfo, error) {
	logger := zap.L()
	logger.Info("enter (Actions).PackagesData")
	defer logger.Info("exit (Actions).PackagesData")
	pkgs, err := a.repo.GetPackages()
	if err != nil {
		return nil, err
	}
	out := make([]PkgInfo, 0, len(pkgs))
	for _, p := range pkgs {
		path := pkgOfID(p.ID)
		if path == "" {
			path = p.Name
		}
		info := PkgInfo{Path: path, Name: p.Name}
		if i := strings.LastIndex(p.Name, "/"); i >= 0 {
			info.Name = p.Name[i+1:]
		}
		if doc := PackageDoc(p); doc != "" {
			info.Doc = doc
		} else if facts, err := a.repo.GetPkgCodeFacts(path); err == nil && facts != nil {
			info.Structs, info.Methods, info.Funcs = facts.Structs, facts.Methods, facts.Funcs
		}
		out = append(out, info)
	}
	return out, nil
}

// PackageDoc 包 doc_comment 清理：去除 Copyright 行（用户要求）——
// `// Copyright ...` / `Copyright (c) ...` 等行跳过；返回清理后文本。
// （R9x 自 cli/wiki_packages.go 迁入——wiki 渲染同源调用。）
func PackageDoc(p *domain.CodeEntity) string {
	dc, ok := p.Properties["doc_comment"].(string)
	if !ok || dc == "" {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(dc, "\n") {
		t := strings.TrimSpace(l)

		t = strings.TrimPrefix(strings.TrimPrefix(t, "//"), "*")
		t = strings.TrimSpace(t)
		if strings.HasPrefix(strings.ToLower(t), "copyright") {
			continue
		}
		lines = append(lines, l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
