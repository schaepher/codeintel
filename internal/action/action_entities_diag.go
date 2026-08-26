package action

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// diagnoseEntities 4 类设计诊断（固定阈值起步，Q6）。
func diagnoseEntities(g *domain.EntityGraph, byID map[string]*domain.EntityNode, pkgMethods map[string]int) []*domain.EntityDiag {

	short := func(id string) string {
		return shortPkg(pkgOfEntityID(id)) + "." + func() string {
			if i := strings.LastIndex(id, ":"); i >= 0 {
				return id[i+1:]
			}
			return id
		}()
	}
	var diags []*domain.EntityDiag

	for _, e := range g.Edges {
		if e.Count >= domain.DiagCoupledMin && pkgOfEntityID(e.From) != pkgOfEntityID(e.To) {
			diags = append(diags, &domain.EntityDiag{
				Kind: domain.DiagCoupled, Target: short(e.From) + "→" + short(e.To),
				Detail: fmt.Sprintf("%d 次方法互调（≥%d）", e.Count, domain.DiagCoupledMin),
			})
		}
	}

	adj := map[string][]string{}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e.To)
	}
	state := map[string]int{}
	var path []string
	var visit func(id string)
	visited := map[string]bool{}
	visit = func(id string) {
		state[id] = 1
		path = append(path, id)
		for _, to := range adj[id] {
			if state[to] == 1 {

				for i, p := range path {
					if p == to {
						cyc := path[i:]
						if len(cyc) >= 2 && pkgOfEntityID(cyc[0]) != pkgOfEntityID(cyc[1]) {
							key := short(cyc[0]) + "↔" + short(cyc[len(cyc)-1])
							if !visited[key] {
								visited[key] = true
								diags = append(diags, &domain.EntityDiag{
									Kind: domain.DiagCycle, Target: key,
									Detail: "跨包实体循环依赖（" + short(cyc[len(cyc)-1]) + "→" + short(cyc[0]) + "）",
								})
							}
						}
						break
					}
				}
				continue
			}
			if state[to] == 0 {
				visit(to)
			}
		}
		path = path[:len(path)-1]
		state[id] = 2
	}
	for id := range byID {
		if state[id] == 0 {
			visit(id)
		}
	}

	for _, n := range g.Nodes {
		if n.Kind == domain.EntityKindPkgFace {
			continue
		}
		if n.MethodCount >= domain.DiagGodMethods || n.OutCalls >= domain.DiagGodOutCalls {
			detail := fmt.Sprintf("%d 方法 / %d 出边", n.MethodCount, n.OutCalls)
			diags = append(diags, &domain.EntityDiag{Kind: domain.DiagGodObject,
				Target: short(n.ID), Detail: detail})
		}
	}

	for _, n := range g.Nodes {
		if n.Kind != domain.EntityKindPkgFace {
			continue
		}
		if n.FreeFuncs >= 8 && n.FreeFuncs > pkgMethods[n.Pkg]+1 {
			diags = append(diags, &domain.EntityDiag{Kind: domain.DiagFaceHeavy,
				Target: short(n.ID),
				Detail: fmt.Sprintf("%d 游离函数 > 包内 %d 个方法——考虑类型封装", n.FreeFuncs, pkgMethods[n.Pkg])})
		}
	}

	sort.Slice(diags, func(i, j int) bool {
		if diags[i].Kind != diags[j].Kind {
			return diags[i].Kind < diags[j].Kind
		}
		return diags[i].Target < diags[j].Target
	})
	return diags
}
