package action

// T16：注册函数投影索引（一次读取，多服务复用）。
//
// grpcRoutes 原本对每个 grpc_service 各查一次 `registers_service` 属性
// （N 个服务 → N 次属性查询）。这里按注册函数投影一次取全量并建 map；
// 只有**旧索引**（整库没有 registers_service 属性——R30 之前的构建）才
// 回退到 `Register<Svc>Server/Handler` 名称匹配，避免把同名但未注册的
// helper 当成路由。

import (
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
)

// routeRegisters 注册函数索引（服务短名 → 注册函数节点）。
type routeRegisters struct {
	byService map[string]*domain.CodeEntity
	// legacy 旧索引：整库没有任何 registers_service 属性时按名称兼容匹配。
	legacy bool
	r      Reader
	// nameMiss 旧索引名称回退的未命中缓存（同一服务只查一次名字）。
	nameMiss map[string]bool
}

// loadRouteRegisters 一次读取注册函数投影。
func loadRouteRegisters(r Reader) *routeRegisters {
	rr := &routeRegisters{byService: map[string]*domain.CodeEntity{}, r: r, nameMiss: map[string]bool{}}
	nodes, err := r.GetRouteRegisterNodes()
	if err != nil || len(nodes) == 0 {
		// 查询失败与空投影都按"旧索引/无属性"处理：名称回退仍是保守的
		// 兼容路径（Registers 属性缺失时才启用）。
		rr.legacy = true
		return rr
	}
	for _, n := range nodes {
		svc := n.Property("registers_service")
		if svc == "" {
			continue
		}
		prev, ok := rr.byService[svc]
		if !ok || registerNodeLess(n, prev) {
			rr.byService[svc] = n
		}
	}
	return rr
}

// registerNodeLess 同一服务的多个注册函数取稳定代表（行号小者优先，
// 再按 canonical ID 字典序）——与 map 迭代顺序无关。
func registerNodeLess(a, b *domain.CodeEntity) bool {
	if a.LineStart != b.LineStart {
		return a.LineStart < b.LineStart
	}
	return string(a.ID) < string(b.ID)
}

// node 取服务对应的注册函数节点（属性优先；旧索引按名称兼容）。
func (rr *routeRegisters) node(svcName string) *domain.CodeEntity {
	if rr == nil || svcName == "" {
		return nil
	}
	if n, ok := rr.byService[svcName]; ok {
		return n
	}
	if !rr.legacy || rr.r == nil || rr.nameMiss[svcName] {
		return nil
	}
	rr.nameMiss[svcName] = true
	for _, name := range []string{"Register" + svcName + "Handler", "Register" + svcName + "Server"} {
		nodes, err := rr.r.GetSymbolByName(name)
		if err != nil {
			continue
		}
		cands := make([]*domain.CodeEntity, 0, len(nodes))
		for _, n := range nodes {
			if n.Kind == domain.KindFunction || n.Kind == domain.KindMethod {
				cands = append(cands, n)
			}
		}
		if len(cands) == 0 {
			continue
		}
		sort.Slice(cands, func(i, j int) bool { return registerNodeLess(cands[i], cands[j]) })
		rr.byService[svcName] = cands[0]
		return cands[0]
	}
	return nil
}

// file 注册函数所在文件（相对仓库根）；无注册函数返回空。
func (rr *routeRegisters) file(svcName string) string {
	if n := rr.node(svcName); n != nil {
		return n.FilePath
	}
	return ""
}
