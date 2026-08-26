package cli

import (
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// R82 架构图层识别增强（从 wiki_arch_layers.go 拆出——行数治理）。

// archSvcPkgs 接入层服务包集合（grpc_service/http_route 节点所在包
// ——R82：识别到 grpc/http 服务就归接入层，不依赖包短名约定）。
func archSvcPkgs(repo *sqlite.Repo) map[string]bool {
	out := map[string]bool{}
	if repo == nil {
		return out
	}
	rows, err := repo.Query(`SELECT id FROM nodes WHERE kind IN ('grpc_service', 'http_route')`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		p := symbolPkg(id)
		if i := strings.LastIndex(p, "/"); i >= 0 {
			p = p[i+1:]
		}
		if p != "" {
			out[p] = true
		}
	}
	return out
}

// archLayerOf 包短名 → 层名（R82：服务包集合命中 → 接入层优先——
// grpc/http 服务所在包即使短名不匹配接入约定也归接入层）。
func archLayerOf(short string, svcPkgs map[string]bool) string {
	if archAccessPkgs[short] || svcPkgs[short] {
		return "接入层"
	}
	return archLayerName(short)
}

// archLayerName 包短名 → 层名（接入/存储；其余领域层）。
func archLayerName(short string) string {
	if archAccessPkgs[short] {
		return "接入层"
	}
	if archStoragePkgs[short] {
		return "存储层"
	}
	return "领域层"
}
