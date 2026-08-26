package cli

// R83 时序图停止包配置（~/.codeintel/config.yaml 的 seq.stop_packages）：
// 命中这些包的被调函数不再深入展开内部——基础设施/第三方封装/样板
// 包内部调用无业务信息，图更聚焦；节点仍显示在图上。

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

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

// seqStopPkgHit 符号 ID 所在包是否命中停止列表（完整路径或短名匹配）。
func seqStopPkgHit(symID string) bool {
	stops := loadSeqStopPkgs()
	if len(stops) == 0 {
		return false
	}
	pkg := symbolPkg(symID)
	short := pkg
	if i := strings.LastIndex(short, "/"); i >= 0 {
		short = short[i+1:]
	}
	for _, s := range stops {
		if pkg == s || short == s || strings.HasSuffix(pkg, "/"+s) {
			return true
		}
	}
	return false
}

// implTypeShort 从被调符号 canonical ID 提取短类型名（R83：参与者第
// 二行——包最后路径段.类型名）：
//   symbol:go:example.com/m/domain/order:(orderManagerImpl).SubmitOrder
//     → order.orderManagerImpl（方法形态取 (T) 的 T）
//   symbol:go:example.com/m/repo:orderRepo → repo.orderRepo（类型形态）
//   函数形态（无类型）→ 空。
func implTypeShort(symID string) string {
	rest := strings.TrimPrefix(symID, "symbol:go:")
	// 方法形态：(Type).Method——类型在括号里
	if i := strings.Index(rest, ":("); i >= 0 {
		pkg := rest[:i]
		t := rest[i+2:]
		if j := strings.Index(t, ")."); j >= 0 {
			t = t[:j]
		}
		return pkgShort(pkg) + "." + t
	}
	// 类型/函数形态：pkg:name
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		pkg := rest[:i]
		name := rest[i+1:]
		if name != "" {
			return pkgShort(pkg) + "." + name
		}
	}
	return ""
}

// pkgShort 包路径最后一段（example.com/m/domain/order → order）。
func pkgShort(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}
