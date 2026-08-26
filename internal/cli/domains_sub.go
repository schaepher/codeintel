package cli

// R80 subdomains 校验（从 domains.go 拆出——行数治理）：AI 输出的
// 子域归属包/表须在事实包中（防编造——剔除无效项保留子域）。

import "fmt"

// sanitizeSubdomains 子域校验：包/表逐项校验（不在事实包剔除并警告）；
// 无有效归属的子域整体剔除。返回清洗后的子域列表 + 警告。
func sanitizeSubdomains(d wikiDomainCfg, havePkg, haveTbl map[string]bool) ([]wikiSubdomainCfg, []string) {
	var subs []wikiSubdomainCfg
	var warns []string
	for _, sd := range d.Subdomains {
		if sd.Name == "" {
			warns = append(warns, fmt.Sprintf("域 %s：跳过无名称的子域", d.Name))
			continue
		}
		var sp, st []string
		for _, p := range sd.Packages {
			if havePkg[p] {
				sp = append(sp, p)
			} else {
				warns = append(warns, fmt.Sprintf("子域 %s/%s：包 %s 不在事实包中（剔除）", d.Name, sd.Name, p))
			}
		}
		for _, t := range sd.Tables {
			if haveTbl[t] {
				st = append(st, t)
			} else {
				warns = append(warns, fmt.Sprintf("子域 %s/%s：表 %s 不在事实包中（剔除）", d.Name, sd.Name, t))
			}
		}
		if len(sp) == 0 && len(st) == 0 {
			warns = append(warns, fmt.Sprintf("子域 %s/%s：无有效归属（剔除）", d.Name, sd.Name))
			continue
		}
		sd.Packages, sd.Tables = sp, st
		subs = append(subs, sd)
	}
	return subs, warns
}
