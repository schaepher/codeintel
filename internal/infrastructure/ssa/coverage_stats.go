package ssa

import (
	"strings"
	"sync"
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
	covMu       sync.Mutex
	covErrPkgs  []PackageError // 具体失败包/错误位置（上限 20 个包 ×3 条）
)

// PackageError 一个失败包的具体错误（todo13-13：只给计数不够，查询要能
// 指出坏包/文件/错误位置与影响范围）。
type PackageError struct {
	Path   string   `json:"path"`
	Errors []string `json:"errors"`
}

// CoverageSnapshot 构建覆盖快照。
type CoverageSnapshot struct {
	Packages         int64          `json:"pkg_total"`
	PackagesWithErrs int64          `json:"pkg_with_errors"`
	TypeErrors       int64          `json:"typecheck_errors"`
	ErrorPackages    []PackageError `json:"error_packages,omitempty"`
}

func resetCoverage() {
	covPkgs.Store(0)
	covPkgErr.Store(0)
	covTypeErrs.Store(0)
	covMu.Lock()
	covErrPkgs = nil
	covMu.Unlock()
}

func recordCoverage(pkgs []*packages.Package) {
	var total, withErr, typeErrs int64
	var details []PackageError
	for _, p := range pkgs {
		if p == nil {
			continue
		}
		total++
		if len(p.Errors) > 0 {
			withErr++
			if len(details) < 20 {
				pe := PackageError{Path: p.PkgPath}
				for _, e := range p.Errors {
					if len(pe.Errors) >= 2 {
						break
					}
					msg := strings.TrimSpace(e.Msg)
					if msg == "" || strings.HasPrefix(msg, "# ") {
						continue // 包级标题行不是具体错误位置
					}
					if e.Pos != "" {
						msg = e.Pos + ": " + msg
					}
					pe.Errors = append(pe.Errors, msg)
				}
				details = append(details, pe)
			}
		}
		typeErrs += int64(len(p.TypeErrors))
	}
	covPkgs.Store(total)
	covPkgErr.Store(withErr)
	covTypeErrs.Store(typeErrs)
	covMu.Lock()
	covErrPkgs = details
	covMu.Unlock()
}

// CoverageStats 当前覆盖快照。
func CoverageStats() CoverageSnapshot {
	covMu.Lock()
	pkgs := append([]PackageError(nil), covErrPkgs...)
	covMu.Unlock()
	return CoverageSnapshot{Packages: covPkgs.Load(),
		PackagesWithErrs: covPkgErr.Load(), TypeErrors: covTypeErrs.Load(),
		ErrorPackages: pkgs}
}
