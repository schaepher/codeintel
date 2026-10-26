package ssa

import (
	"sync/atomic"

	"golang.org/x/tools/go/packages"
)

// ---- todo12-13 R3：构建覆盖/类型检查失败可观测 ----
// 构建曾长时间报 success，但 stderr 有类型检查错误、字段类型回退——
// "success" 只代表流程完成。这里把包/类型检查覆盖写入 build_metadata，
// 与 SQL 解析统计分开；查询侧（sequence meta.coverage）原样携带。

var (
	covPkgs     atomic.Int64 // 加载的包数（SSA 看到的模块包）
	covPkgErr   atomic.Int64 // 带 Errors 的包数
	covTypeErrs atomic.Int64 // 类型检查错误条数
)

// CoverageSnapshot 构建覆盖快照。
type CoverageSnapshot struct {
	Packages         int64 `json:"pkg_total"`
	PackagesWithErrs int64 `json:"pkg_with_errors"`
	TypeErrors       int64 `json:"typecheck_errors"`
}

func resetCoverage() {
	covPkgs.Store(0)
	covPkgErr.Store(0)
	covTypeErrs.Store(0)
}

func recordCoverage(pkgs []*packages.Package) {
	var total, withErr, typeErrs int64
	for _, p := range pkgs {
		if p == nil {
			continue
		}
		total++
		if len(p.Errors) > 0 {
			withErr++
		}
		typeErrs += int64(len(p.TypeErrors))
	}
	covPkgs.Store(total)
	covPkgErr.Store(withErr)
	covTypeErrs.Store(typeErrs)
}

// CoverageStats 当前覆盖快照。
func CoverageStats() CoverageSnapshot {
	return CoverageSnapshot{Packages: covPkgs.Load(),
		PackagesWithErrs: covPkgErr.Load(), TypeErrors: covTypeErrs.Load()}
}
