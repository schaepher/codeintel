package ssa

// todo9-3：包缓存按**分析范围**隔离。
//
// 原有缓存键只包含"本包源码 + 直接依赖源码"——单包分析（增量 update 只
// Load 变更包）与整仓分析会得到相同键，可能命中彼此不完整的缓存：例如
// 单包分析时包 A 里"只看到 A 加载范围"的产物（dispatch/类型池/摘要传播
// 依赖被分析的包集合）被整仓构建复用。
//
// 范围指纹（构建级一次）：模块集合、加载包集合、各包源码内容、语法树
// 数量、类型错误状态；模块与包按稳定顺序拼接，不修改调用方切片。范围
// 指纹与原包缓存键组合后用于读写；任一输入变化 → 键变化（旧缓存自然
// 失效，无需改缓存文件协议/DB schema）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"sort"

	"golang.org/x/tools/go/packages"
)

// analysisScope 构建级分析范围指纹 + 源码内容 memo（供 pkgCacheKeyHash 复用，
// 避免范围指纹与包哈希各读一遍文件）。
type analysisScope struct {
	fingerprint string
	contentMemo map[string]string
}

// buildAnalysisScopeFn 测试可替换（覆盖"指纹计算失败 → 禁用缓存"路径）。
var buildAnalysisScopeFn = buildAnalysisScope

// buildAnalysisScope 计算一次分析范围指纹（不修改 pkgs/modules）。
func buildAnalysisScope(ctx context.Context, pkgs []*packages.Package, modules []string) (*analysisScope, error) {
	// 稳定顺序：复制后排序（调用方切片保持不变）。
	mods := append([]string(nil), modules...)
	sort.Strings(mods)
	list := append([]*packages.Package(nil), pkgs...)
	sort.SliceStable(list, func(i, j int) bool {
		if list[i] == nil || list[j] == nil {
			return list[j] != nil
		}
		return list[i].PkgPath < list[j].PkgPath
	})

	h := sha256.New()
	scopeField(h, "scope-v1")
	for _, m := range mods {
		scopeField(h, "module")
		scopeField(h, m)
	}
	memo := map[string]string{}
	for _, p := range list {
		if p == nil {
			continue
		}
		sum, err := pkgSourceHash(ctx, p, memo)
		if err != nil {
			return nil, err
		}
		scopeField(h, "pkg")
		scopeField(h, p.PkgPath)
		scopeField(h, sum)
		scopeField(h, "syntax="+itoa(len(p.Syntax)))
		scopeField(h, "files="+itoa(len(p.CompiledGoFiles)))
		scopeField(h, "errors="+itoa(len(p.Errors)))
		scopeField(h, "typeerrors="+itoa(len(p.TypeErrors)))
		if p.IllTyped {
			scopeField(h, "illtyped=1")
		} else {
			scopeField(h, "illtyped=0")
		}
	}
	return &analysisScope{fingerprint: hex.EncodeToString(h.Sum(nil)), contentMemo: memo}, nil
}

// scopeField 写入 hash（0 分隔，避免拼接歧义）。
func scopeField(h hash.Hash, s string) {
	h.Write([]byte(s))
	h.Write([]byte{0})
}

// pkgSourceHash 包源码内容 hash（memo 按 PkgPath 复用；空路径不缓存）。
func pkgSourceHash(ctx context.Context, pkg *packages.Package, memo map[string]string) (string, error) {
	if pkg == nil {
		return "", nil
	}
	if v, ok := memo[pkg.PkgPath]; ok && pkg.PkgPath != "" {
		return v, nil
	}
	files := pkg.CompiledGoFiles
	if len(files) == 0 {
		files = pkg.GoFiles
	}
	sum, err := pkgContentHash(ctx, files)
	if err != nil {
		return "", err
	}
	if pkg.PkgPath != "" {
		memo[pkg.PkgPath] = sum
	}
	return sum, nil
}

// combineScopeKey 范围指纹 + 包键 → 最终缓存键（改变即缓存失效）。
func combineScopeKey(scope, pkgKey string) string {
	h := sha256.Sum256([]byte(scope + "\x00" + pkgKey))
	return hex.EncodeToString(h[:])
}

// itoa 小整数转字符串（避免 fmt 依赖/分配模式统一）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
