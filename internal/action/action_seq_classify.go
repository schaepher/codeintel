package action

// todo13-13：调用点未解析/接口无实现/索引缺口的消费者分类。
//
// 目标（用户视角）：看到一条"没展开"的调用时能区分——
//   - 正常叶子（no_calls，业务流程结束）
//   - 外部库目标（external_target：调用表达式已知，库内实现未展开）
//   - 未知实现（unknown_implementation：接口身份已知、无可证明实现）
//   - 索引缺失（index_gap：应在索引内但目标/包缺失）
//   - 分析停止（depth/stop_pkg/cycle/budget/query_failed）

import (
	"context"
	"go/ast"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// isIfaceMethodID 接口方法 canonical ID（symbol:go:<pkg>:(<Type>).<Method>
// 且 <Type> 节点 kind=interface）。具体实现的 (Impl).Method 也含 ":("，
// 不能混为一谈（todo13-13 回归：链式调用被误判为"接口无实现"）。
func (a *Actions) isIfaceMethodID(id string) bool {
	i := strings.Index(id, ":(")
	if i < 0 {
		return false
	}
	rest := id[i+2:]
	j := strings.Index(rest, ").")
	if j < 0 {
		return false
	}
	typeID := id[:i] + ":" + rest[:j]
	n, err := a.repo.GetSymbol(domain.CanonicalID(typeID))
	if err != nil || n == nil {
		return false
	}
	return n.Kind == domain.KindInterface
}

// indexedPkgSet 已索引包路径集合（包节点 symbol:go:<pkg>:<name>）。
//
// todo21-14.2：接收请求 ctx——包清单查询必须能被查询预算取消（原实现
// 无 ctx，连接等待/大库扫表期间不可中断）。取消由调用方判定为
// ErrSeqCanceled，不把空包集合当完整结果继续输出。
func (a *Actions) indexedPkgSet(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	pkgs, err := a.repo.GetPackagesContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		id := string(p.ID)
		id = strings.TrimPrefix(id, "symbol:go:")
		if i := strings.LastIndex(id, ":"); i >= 0 {
			id = id[:i]
		}
		if id != "" {
			out[id] = true
		}
	}
	return out, nil
}

// seqCoverageDiagnostics 构建覆盖诊断：具体失败包 + 目标是否在范围内 +
// SQL 降级（与包失败分开）。目标隔离只能按包声明，不声称完全隔离。
func seqCoverageDiagnostics(cov *SeqCoverage, targetPkg string) []SeqDiagCount {
	if cov == nil {
		return nil
	}
	var out []SeqDiagCount
	for i, pe := range cov.ErrorPackages {
		if i >= 10 {
			break
		}
		detail := pe.Path
		if len(pe.Errors) > 0 {
			detail += "：" + strings.Join(pe.Errors, "；")
		}
		out = append(out, SeqDiagCount{Code: SeqDegradedPackage, Count: 1,
			Detail: "构建期失败包 " + detail})
	}
	if len(cov.ErrorPackages) > 0 {
		hit := ""
		for _, pe := range cov.ErrorPackages {
			if targetPkg != "" && (pe.Path == targetPkg || strings.HasPrefix(targetPkg, pe.Path+"/")) {
				hit = pe.Path
				break
			}
		}
		if hit != "" {
			out = append(out, SeqDiagCount{Code: SeqTargetCoverage, Count: 1,
				Detail: "目标包 " + targetPkg + " 在失败包列表（" + hit + "）——目标分析不完整；跨包依赖无法完全排除"})
		} else if targetPkg != "" {
			out = append(out, SeqDiagCount{Code: SeqTargetCoverage, Count: 1,
				Detail: "目标包 " + targetPkg + " 不在失败包列表（按包隔离）；跨包类型/依赖解析失败仍可能影响，无法完全证明隔离"})
		}
	}
	if n := coverageNum(cov.DegradeStats, "sql_ast_fail"); n > 0 {
		out = append(out, SeqDiagCount{Code: SeqSQLDegraded, Count: int(n),
			Detail: "SQL AST 解析失败（启发式回退）——SQL 语义覆盖不完整，与包失败分开记录"})
	}
	return out
}

// callImportPath 选择器调用的导入包路径（别名/默认名解析）；非包选择器返回空。
func callImportPath(f *ast.File, fun ast.Expr) string {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || f == nil {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p == "" {
			continue
		}
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "" {
			if i := strings.LastIndex(p, "/"); i >= 0 {
				name = p[i+1:]
			} else {
				name = p
			}
		}
		if name == id.Name {
			return p
		}
	}
	return ""
}

// classifyUnresolvedCall 未解析调用 → (code, detail)。indexed = 已索引包路径集合。
func classifyUnresolvedCall(f *ast.File, fun ast.Expr, curPkg string, indexed map[string]bool) (string, string) {
	path := callImportPath(f, fun)
	if path == "" {
		return SeqStopUnresolved, "调用目标未解析（非包选择器/局部目标；调用表达式已知）"
	}
	if indexed != nil && (indexed[path] || indexedPkgPrefix(indexed, path)) {
		return SeqStopIndexGap, "包 " + path + " 已索引，但调用目标未解析（目标符号缺失/生成符号）"
	}
	if sameModulePrefix(path, curPkg) {
		return SeqStopIndexGap, "导入包 " + path + " 与当前包同属一个 module，但未在索引覆盖内（构建覆盖缺口）"
	}
	return SeqStopExternalTarget, "外部库 " + path + "（调用表达式已证明；库内实现未展开，不等于索引缺失）"
}

// indexedPkgPrefix 导入路径是否为已索引包的前缀（子包场景）。
func indexedPkgPrefix(indexed map[string]bool, path string) bool {
	for p := range indexed {
		if strings.HasPrefix(p, path+"/") {
			return true
		}
	}
	return false
}

// sameModulePrefix 两个 import path 是否属于同一 module（前两段相同；
// module 根常为 <domain>/<name>）。
func sameModulePrefix(a, b string) bool {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	n := 2
	if len(as) < n {
		n = len(as)
	}
	if len(bs) < n {
		n = len(bs)
	}
	if n == 0 {
		return false
	}
	for i := 0; i < n; i++ {
		if as[i] != bs[i] {
			return false
		}
	}
	return strings.Contains(as[0], ".")
}
