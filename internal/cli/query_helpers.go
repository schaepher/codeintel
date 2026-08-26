package cli

// R88 `query helpers [--min-packages N]`——工具函数清单：游离函数
// （包级函数非方法）且被 ≥N 个包调用（N 默认 3；~/.codeintel/
// config.yaml 的 helpers.min_packages 可调）。wiki 枚举与工具函数
// 区块同源读取（renderEnumsMD/HTML 共用 queryHelpers）。

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"gopkg.in/yaml.v3"
)

// helperEntry 工具函数条目。
type helperEntry struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Pkgs    int    `json:"pkgs"`    // 调用方所在包数（去重）
	Callers int    `json:"callers"` // 总调用方数
}

// helperMinPackages 配置 helpers.min_packages（默认 3）。
func helperMinPackages() int {
	p := agentConfigPath()
	if p == "" {
		return 3
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return 3
	}
	var c struct {
		Helpers struct {
			MinPackages int `yaml:"min_packages"`
		} `yaml:"helpers"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return 3
	}
	if c.Helpers.MinPackages > 0 {
		return c.Helpers.MinPackages
	}
	return 3
}

// queryHelpers 工具函数：游离函数（kind=function 非方法）+ 调用方
// 所在包去重数 ≥ minPkgs。一次加载 calls 边内存统计（避免逐符号
// 查询）。排除 _test.go。
func queryHelpers(r *sqlite.Repo, minPkgs int) []helperEntry {
	// 1. 游离函数集合（kind=function——方法为 kind=method）
	fns := map[string]string{} // id → name
	rows, err := r.Query(`SELECT id, name FROM nodes
		WHERE kind = 'function' AND file_path NOT LIKE '%_test.go' AND file_path NOT LIKE '../%'`)
	if err != nil {
		return nil
	}
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			fns[id] = name
		}
	}
	rows.Close()
	if len(fns) == 0 {
		return nil
	}
	// 2. calls 入边：调用方 → 游离函数；调用方按包去重计数
	callersOf := map[string]map[string]bool{} // 函数 id → 调用方包集合
	callerCnt := map[string]int{}
	rows2, err := r.Query(`SELECT source_id, target_id FROM edges WHERE kind = 'calls'`)
	if err != nil {
		return nil
	}
	for rows2.Next() {
		var src, tgt string
		if rows2.Scan(&src, &tgt) != nil || fns[tgt] == "" {
			continue
		}
		pkg := pkgOfID(src)
		if pkg == "" {
			continue
		}
		if callersOf[tgt] == nil {
			callersOf[tgt] = map[string]bool{}
		}
		if !callersOf[tgt][pkg] {
			callersOf[tgt][pkg] = true
		}
		callerCnt[tgt]++
	}
	rows2.Close()
	var out []helperEntry
	for id, name := range fns {
		pkgs := len(callersOf[id])
		if pkgs < minPkgs {
			continue
		}
		out = append(out, helperEntry{Name: name, ID: id, Pkgs: pkgs, Callers: callerCnt[id]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pkgs != out[j].Pkgs {
			return out[i].Pkgs > out[j].Pkgs
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// pkgOfID canonical ID → 包路径（symbol:go:<pkg>:<name> → <pkg>；
// 方法 ID symbol:go:<pkg>:(T).m 同样取 pkg）。
func pkgOfID(id string) string {
	rest := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.LastIndex(rest, ":"); i > 0 {
		return rest[:i]
	}
	return ""
}

// cmdQueryHelpers 实现 `query helpers [--min-packages N] [--json]`。
func cmdQueryHelpers(r *sqlite.Repo, minPkgs int, jsonOut bool) int {
	if minPkgs <= 0 {
		minPkgs = helperMinPackages()
	}
	out := queryHelpers(r, minPkgs)
	if jsonOut {
		encodeJSON(out)
		return 0
	}
	if len(out) == 0 {
		fmt.Printf("未找到被 >=%d 个包调用的工具函数\n", minPkgs)
		return 0
	}
	fmt.Printf("== 工具函数（游离函数，被 >=%d 个包调用）==\n", minPkgs)
	for _, h := range out {
		fmt.Printf("  %-28s 包数=%d 调用=%d\n", h.Name, h.Pkgs, h.Callers)
	}
	return 0
}
