package cli

// R77 `query packages`——包结构（wiki 包结构节转命令）：包路径 +
// doc_comment（去 Copyright）+ 无说明时的包内代码事实（结构体/方法/
// 函数签名表格）——wiki index 包结构节同款数据。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// pkgInfo 一个包的结构信息（输出契约）。
type pkgInfo struct {
	Path    string   `json:"path"`    // 包路径（module 内）
	Name    string   `json:"name"`    // 短名（路径末段）
	Doc     string   `json:"doc"`     // 包 doc_comment（去 Copyright；空 = 无包级说明）
	Structs []string `json:"structs"` // 无 doc 时：包内结构体（字段数）
	Methods []string `json:"methods"` // 无 doc 时：方法签名（截断）
	Funcs   []string `json:"funcs"`   // 无 doc 时：函数签名（截断）
}

// packagesData 收集全部包的结构信息（cmd 与 MCP 共用）。
func packagesData(acts *action.Actions, repo *sqlite.Repo) []pkgInfo {
	pkgs, err := acts.Packages()
	if err != nil {
		return nil
	}
	out := make([]pkgInfo, 0, len(pkgs))
	for _, p := range pkgs {
		path := symbolPkg(string(p.ID))
		if path == "" {
			path = p.Name
		}
		info := pkgInfo{Path: path, Name: p.Name}
		if i := strings.LastIndex(p.Name, "/"); i >= 0 {
			info.Name = p.Name[i+1:]
		}
		if doc := packageDoc(p); doc != "" {
			info.Doc = doc
		} else if facts := pkgCodeFactsFor(repo, path); facts != nil {
			info.Structs, info.Methods, info.Funcs = facts.Structs, facts.Methods, facts.Funcs
		}
		out = append(out, info)
	}
	return out
}

// cmdQueryPackages 实现 `query packages [--json|--compact]`。
func cmdQueryPackages(acts *action.Actions, opts outputOpts) int {
	abs, _, err := resolveRepo(opts.repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	repo := sqlite.NewRepo(db)
	pkgs := packagesData(acts, repo)
	if opts.json {
		encodeJSON(pkgs)
		return 0
	}
	if len(pkgs) == 0 {
		fmt.Println("（无包节点——可能未重建索引）")
		return 0
	}
	for _, p := range pkgs {
		fmt.Printf("## %s\n\n", p.Path)
		if p.Doc != "" {
			fmt.Println(p.Doc + "\n")
			continue
		}
		fmt.Println("（无包级说明——代码事实）")
		if len(p.Structs) > 0 {
			fmt.Println("结构体：" + strings.Join(p.Structs, "、"))
		}
		if len(p.Methods) > 0 {
			fmt.Println("方法：")
			for _, m := range p.Methods {
				fmt.Println("  - " + m)
			}
		}
		if len(p.Funcs) > 0 {
			fmt.Println("函数：")
			for _, fn := range p.Funcs {
				fmt.Println("  - " + fn)
			}
		}
		fmt.Println()
	}
	fmt.Printf("共 %d 个包\n", len(pkgs))
	return 0
}
