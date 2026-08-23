package action

// R9 实体协作图聚合：类型（struct/interface）为实体 + 游离函数按包
// 聚合为门面实体；calls 边映射到实体归属后聚合计数（实体间边 +
// 实体内互调）。输出 4 类设计诊断（高耦合/循环/上帝对象/游离占比）
// ——从函数级调用链上移到对象协作层面，反向暴露设计问题。

import (
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// Entities 实体协作图（R9）：仓库级全量聚合——只保留根 module 前缀的
// 包（排除第三方依赖与 fixture/临时 module；examples/scripts 是演示
// 与辅助脚本，无设计价值也排除）。
func (a *Actions) Entities() (*domain.EntityGraph, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Entities")
	defer logger.Info("exit (Actions).Entities")
	raw, err := a.repo.GetEntityRaw()
	if err != nil {
		return nil, err
	}
	mods := a.modules()
	rootMod := ""
	if len(mods) > 0 {
		rootMod = mods[0]
	}
	return aggregateEntities(raw, func(pkg string) bool {
		if rootMod == "" {
			return true
		}
		if pkg != rootMod && !strings.HasPrefix(pkg, rootMod+"/") {
			return false
		}
		return !strings.Contains(pkg, "/examples/") && !strings.Contains(pkg, "/scripts/")
	}), nil
}

// aggregateEntities 纯函数聚合（可单测）：实体提取 → 过滤 → 边映射
// → 诊断。keep 决定哪些包参与（nil = 全部保留）。
func aggregateEntities(raw *domain.EntityRaw, keep func(string) bool) *domain.EntityGraph {
	if keep == nil {
		keep = func(string) bool { return true }
	}
	// 1. 实体索引：类型实体（有方法才算） + 包门面实体（游离函数 ≥ 5）
	byID := map[string]*domain.EntityNode{}
	pkgTypeMethods := map[string]int{}	// 包 → 方法总数（face-heavy 诊断）
	pkgFreeFuncs := map[string]int{}	// 包 → 游离函数数
	var pkgNameOf = func(id string) string { return pkgOfEntityID(id) }

	for _, t := range raw.Types {
		pkg := pkgNameOf(string(t.ID))
		if !keep(pkg) {
			continue
		}
		kind := t.Kind
		if kind != domain.KindInterface {
			kind = domain.EntityKindStruct
		}
		byID[string(t.ID)] = &domain.EntityNode{
			ID:	string(t.ID), Name: t.Name, Pkg: pkg,
			Kind:	string(kind),
		}
	}
	// has_method → 方法数（无方法的类型在最后剔除——先统计后过滤；
	// 只记录 keep 包内的类型——被排除类型的方法不得映射回不存在的节点）
	methodToType := map[string]string{}
	for _, hm := range raw.HasM {
		src, dst := string(hm.SourceID), string(hm.TargetID)
		if n, ok := byID[src]; ok {
			n.MethodCount++
			pkgTypeMethods[n.Pkg]++
			methodToType[dst] = src
		}
	}
	// 游离函数（function 节点）→ 包计数；包门面实体（仅 keep 包）
	for _, f := range raw.Funcs {
		pkg := pkgNameOf(string(f.ID))
		if !keep(pkg) {
			continue
		}
		pkgFreeFuncs[pkg]++
	}
	for pkg, cnt := range pkgFreeFuncs {
		if cnt >= domain.FaceMinFreeFuncs {
			byID["symbol:go:"+pkg+":"+shortPkg(pkg)] = &domain.EntityNode{
				ID:	"symbol:go:" + pkg + ":" + shortPkg(pkg), Name: shortPkg(pkg),
				Pkg:	pkg, Kind: domain.EntityKindPkgFace, FreeFuncs: cnt,
			}
		}
	}
	// 2. calls 边映射实体 + 聚合
	type ekey struct{ from, to string }
	edges := map[ekey]int{}
	entityOf := map[string]string{}
	entityOfID := func(id string) string {
		if n, ok := byID[id]; ok {
			return n.ID	// 类型本身
		}
		if t, ok := methodToType[id]; ok {
			if _, ok := byID[t]; ok {
				return t	// 归属类型未被行为门槛过滤
			}
			return ""
		}
		pkg := pkgNameOf(id)
		if !keep(pkg) {
			return ""
		}
		if pkgFreeFuncs[pkg] >= domain.FaceMinFreeFuncs {
			return "symbol:go:" + pkg + ":" + shortPkg(pkg)
		}
		return ""
	}
	for _, c := range raw.Calls {
		src, dst := entityOfID(string(c.SourceID)), entityOfID(string(c.TargetID))
		if src == "" || dst == "" {
			continue
		}
		entityOf[string(c.SourceID)] = src
		entityOf[string(c.TargetID)] = dst
		if src == dst {
			byID[src].InnerCalls++
			continue
		}
		edges[ekey{src, dst}]++
		byID[src].OutCalls++
	}

	// 3. 行为门槛过滤（聚合后——OutCalls 已算）：无方法（纯数据
	// 载体）或 1 方法 0 出边（MCP 参数 DTO/缓存结构）无协作语义，
	// 不进实体图；同步清理指向被滤实体的边。接口豁免——依赖契约
	// 语义（接口方法不在索引 has_method，MethodCount 统计不上）
	for id, n := range byID {
		if n.Kind == domain.EntityKindPkgFace || n.Kind == domain.EntityKindIface {
			continue
		}
		if n.MethodCount == 0 || (n.MethodCount <= 1 && n.OutCalls == 0) {
			delete(byID, id)
		}
	}
	for k := range edges {
		if _, ok := byID[k.from]; !ok {
			delete(edges, k)
		}
	}

	// 4. 确定性输出
	g := &domain.EntityGraph{}
	for _, n := range byID {
		g.Nodes = append(g.Nodes, n)
	}
	sort.Slice(g.Nodes, func(i, j int) bool {
		if g.Nodes[i].Pkg != g.Nodes[j].Pkg {
			return g.Nodes[i].Pkg < g.Nodes[j].Pkg
		}
		return g.Nodes[i].Name < g.Nodes[j].Name
	})
	keys := make([]ekey, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].from != keys[j].from {
			return keys[i].from < keys[j].from
		}
		return keys[i].to < keys[j].to
	})
	for _, k := range keys {
		g.Edges = append(g.Edges, &domain.EntityEdge{From: k.from, To: k.to, Count: edges[k]})
	}
	g.Diags = diagnoseEntities(g, byID, pkgTypeMethods)
	g.EntityOf = entityOf
	// ByName：方法/游离函数短名 → 实体（渲染层短名映射，跨包重名取全部）
	byName := map[string][]string{}
	addName := func(short, eid string) {
		if short == "" {
			return
		}
		byName[short] = append(byName[short], eid)
	}
	for id, eid := range entityOf {
		addName(shortNameOf(id), eid)
	}
	for _, n := range g.Nodes {
		addName(n.Name, n.ID)
	}
	g.ByName = byName
	return g
}

// shortNameOf canonical ID 末段（方法/函数短名）。
func shortNameOf(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// diagnoseEntities 4 类设计诊断（固定阈值起步，Q6）。

// pkgOfEntityID canonical ID → 包路径（symbol:go:<pkg>:<name>）。
func pkgOfEntityID(id string) string {
	rest := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// shortPkg 包路径末段（门面实体名）。
func shortPkg(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}
