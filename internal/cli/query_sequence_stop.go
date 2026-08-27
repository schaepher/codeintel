package cli

// R83 时序图停止包配置（~/.codeintel/config.yaml 的 seq.stop_packages）：
// 命中这些包的被调函数不再深入展开内部——基础设施/第三方封装/样板
// 包内部调用无业务信息，图更聚焦；节点仍显示在图上。
// R95：命中判定迁 action（seqStopPkgHit——StopPackages 经
// CodeSequenceRequest 传入）；本文件只留配置读取（cli 层）。

import (
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"gopkg.in/yaml.v3"
)

// loadSeqDepth 全局配置 seq.depth（R83：wiki grpc 方法代码级时序的
// 嵌套层级；默认 3；wiki --seq-depth 参数优先）。
func loadSeqDepth() int {
	p := agentConfigPath()
	if p == "" {
		return 3
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return 3
	}
	var c struct {
		Seq struct {
			Depth int `yaml:"depth"`
		} `yaml:"seq"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return 3
	}
	if c.Seq.Depth > 0 {
		return c.Seq.Depth
	}
	return 3
}

// loadSeqStopPkgs 读全局配置 seq.stop_packages（每次读取——配置文件
// 小；agentConfigPath 可覆盖便于测试）。
func loadSeqStopPkgs() []string {
	p := agentConfigPath()
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var c struct {
		Seq struct {
			StopPackages []string `yaml:"stop_packages"`
		} `yaml:"seq"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil
	}
	var out []string
	for _, s := range c.Seq.StopPackages {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// loadSeqFilter 读全局配置 seq.filter_pkgs/filter_files/filter_fns/
// filter_regex（S5——导出时过滤调用，如 log）。
func loadSeqFilter() action.SeqFilter {
	p := agentConfigPath()
	if p == "" {
		return action.SeqFilter{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return action.SeqFilter{}
	}
	var c struct {
		Seq struct {
			FilterPkgs  []string `yaml:"filter_pkgs"`
			FilterFiles []string `yaml:"filter_files"`
			FilterFns   []string `yaml:"filter_fns"`
			FilterRegex []string `yaml:"filter_regex"`
		} `yaml:"seq"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return action.SeqFilter{}
	}
	return action.SeqFilter{Pkgs: c.Seq.FilterPkgs, Files: c.Seq.FilterFiles,
		Fns: c.Seq.FilterFns, Regex: c.Seq.FilterRegex}
}
