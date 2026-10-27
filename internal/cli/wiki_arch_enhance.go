package cli

// R82 架构图层识别（从 wiki_arch_layers.go 拆出——行数治理）。
// R100 起接入层服务包识别经 action（Actions.ArchSvcPkgs）；此处只剩
// 纯渲染期的层名判定。

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
