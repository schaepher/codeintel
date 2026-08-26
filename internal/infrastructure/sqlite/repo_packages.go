package sqlite

// R9x：query packages 代码事实查询窄方法（原 cli/wiki_packages.go 的
// pkgCodeFactsFor 直连 SQL 收口到仓储层）——包内 struct/function/
// method 节点清单（无包级 doc_comment 时的 fallback 数据）。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetPkgCodeFacts 包内代码事实：结构体（字段数）/方法/函数签名。
// SQL 按符号 ID 前缀取包下 struct/function/method（kind 过滤 + 上限）。
func (r *Repo) GetPkgCodeFacts(pkgPath string) (*domain.PkgCodeFacts, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetPkgCodeFacts")
	defer logger.Debug("exit (Repo).GetPkgCodeFacts")
	rows, err := r.Query(`SELECT name, kind, COALESCE(json_extract(properties, '$.signature'), ''), COALESCE(json_extract(properties, '$.fields'), '')
		FROM nodes WHERE id LIKE ? AND kind IN ('struct','function','method')
		ORDER BY kind, name LIMIT 80`, "symbol:go:"+pkgPath+":%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &domain.PkgCodeFacts{}
	for rows.Next() {
		var name, kind, sig, fields string
		if err := rows.Scan(&name, &kind, &sig, &fields); err != nil {
			continue
		}
		switch kind {
		case "struct":
			n := strings.Count(fields, `"name"`)
			if n > 0 {
				out.Structs = append(out.Structs, fmt.Sprintf("%s（字段 %d）", name, n))
			} else {
				out.Structs = append(out.Structs, name)
			}
		case "method":
			if sig != "" {
				out.Methods = append(out.Methods, name+sigShort(sig))
			} else {
				out.Methods = append(out.Methods, name)
			}
		case "function":
			if sig != "" {
				out.Funcs = append(out.Funcs, name+sigShort(sig))
			} else {
				out.Funcs = append(out.Funcs, name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Structs) > 12 {
		out.Structs = out.Structs[:12]
	}
	if len(out.Methods) > 20 {
		out.Methods = out.Methods[:20]
	}
	if len(out.Funcs) > 10 {
		out.Funcs = out.Funcs[:10]
	}
	return out, nil
}

// sigShort 签名截断（首行 + 60 runes——长签名压行）。
func sigShort(sig string) string {
	if i := strings.IndexByte(sig, '\n'); i >= 0 {
		sig = sig[:i]
	}
	if r := []rune(sig); len(r) > 60 {
		sig = string(r[:60]) + "…"
	}
	return sig
}
