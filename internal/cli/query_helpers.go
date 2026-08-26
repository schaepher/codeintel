package cli

// R88/R89 `query helpers [--min-packages N] [--json]`——工具函数清单。
// 业务逻辑在 action（Actions.Helpers——游离函数 + 跨包使用数 ≥N）；
// cli 只做：配置读取（helpers.min_packages 默认 3）+ 参数转发 + 输出。

import (
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"gopkg.in/yaml.v3"
)

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

// cmdQueryHelpers 实现 `query helpers [--min-packages N] [--json]`。
func cmdQueryHelpers(r *sqlite.Repo, minPkgs int, jsonOut bool) int {
	if minPkgs <= 0 {
		minPkgs = helperMinPackages()
	}
	acts := action.New(r)
	out, err := acts.Helpers(action.HelpersRequest{MinPackages: minPkgs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
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
