package action

// R89 helpers 命令业务逻辑：工具函数 = 游离函数（kind=function 非
// 方法）且被 ≥N 个包调用（同包多调用方去重）。cli 只做参数解析与
// 输出——本层返回结构化结果（wiki 枚举区块同源调用）。

import (
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// HelpersRequest query helpers 参数（MinPackages ≤0 = 默认 3——
// 配置加载在 cli 层完成）。
type HelpersRequest struct {
	MinPackages int
}

// HelperEntry 工具函数条目（结构化结果——cli/serve/wiki 展示用）。
type HelperEntry struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Pkgs    int    `json:"pkgs"`    // 调用方所在包数（去重）
	Callers int    `json:"callers"` // 总调用方数
}

// Helpers 工具函数清单：游离函数 + 调用方所在包去重数 ≥ MinPackages。
// 一次加载全部函数与 calls 边内存统计（避免逐符号查询卡死）。
func (a *Actions) Helpers(req HelpersRequest) ([]HelperEntry, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Helpers", zap.Int("min_packages", req.MinPackages))
	defer logger.Info("exit (Actions).Helpers")
	minPkgs := req.MinPackages
	if minPkgs <= 0 {
		minPkgs = 3
	}
	fns, err := a.repo.GetFunctions()
	if err != nil {
		return nil, err
	}
	fnSet := map[domain.CanonicalID]string{}
	for _, n := range fns {
		fnSet[n.ID] = n.Name
	}
	if len(fnSet) == 0 {
		return nil, nil
	}
	calls, err := a.repo.GetAllCalls()
	if err != nil {
		return nil, err
	}
	// 调用方按包去重统计（calls 边 source 的包前缀）
	callersOf := map[domain.CanonicalID]map[string]bool{}
	callerCnt := map[domain.CanonicalID]int{}
	for _, c := range calls {
		if fnSet[c.TargetID] == "" {
			continue
		}
		pkg := pkgPathOf(string(c.SourceID))
		if pkg == "" {
			continue
		}
		if callersOf[c.TargetID] == nil {
			callersOf[c.TargetID] = map[string]bool{}
		}
		if !callersOf[c.TargetID][pkg] {
			callersOf[c.TargetID][pkg] = true
		}
		callerCnt[c.TargetID]++
	}
	var out []HelperEntry
	for id, name := range fnSet {
		pkgs := len(callersOf[id])
		if pkgs < minPkgs {
			continue
		}
		out = append(out, HelperEntry{Name: name, ID: string(id), Pkgs: pkgs, Callers: callerCnt[id]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pkgs != out[j].Pkgs {
			return out[i].Pkgs > out[j].Pkgs
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// pkgPathOf canonical ID → 包路径（symbol:go:<pkg>:<name> → <pkg>；
// 方法 ID symbol:go:<pkg>:(T).m 同样取 pkg）。
func pkgPathOf(id string) string {
	rest := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.LastIndex(rest, ":"); i > 0 {
		return rest[:i]
	}
	return ""
}
