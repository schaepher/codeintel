package sqlite

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// 函数内节点的 ID 前缀范围（P1-11）。
//
// 背景：SSA 发射的函数内节点（ssa_value/parameter/field_access/表列虚拟
// 节点/常量…）ID 一律是 `funcID + "#" + slot`（fe_value.go / fe_nodes.go /
// alias_ids.go / adapter_signature.go / summary_*.go），properties.func_id
// 就是该 funcID。Q246 起 `idx_nodes_func_id` 是为
// `json_extract(properties,'$.func_id') = ?` 建的表达式索引——每次节点
// 插入都要重算 JSON 路径并随机插入 B 树，go2o 实测 9.9MB（8.7% 库体量）。
//
// 既然 funcID 是节点 ID 的**字面前缀**，等值查询可以换成 nodes.id 唯一
// 索引（sqlite_autoindex_nodes_1）上的前缀范围扫：
//
//	id >= funcID+"#"  AND  id < funcID+"$"
//
// 语义证明（BINARY 排序即字节序）：任何以 funcID+"#" 开头的串都 ≥ 下界
// （更长，且前缀相同），且 < 上界（'#'=0x23 < '$'=0x24）；反之区间内的串
// 必须在 funcID 之后紧跟 '#'——恰好是前缀条件。'$' 不是合法 ID 字符，
// 不会误纳端点。
//
// 不变量守卫：SaveBatchStats 写节点时校验（带 func_id 的节点 ID 必须以
// funcID+"#" 开头），违反即报错——否则范围查会静默漏节点。
func funcIDPrefixBounds(funcID string) (lo, hi string) {
	return funcID + "#", funcID + "$"
}

// validateFuncIDPrefix 校验 P1-11 的构造不变量：带 func_id 属性的节点 ID
// 必须以 funcID+"#" 开头（SSA 发射端统一构造；见 funcid_range.go 顶部）。
// 违反时前缀范围查会静默漏掉该节点，因此在写入入口 fail-closed。
func validateFuncIDPrefix(n *domain.CodeEntity) error {
	// 只有"函数内节点"参与前缀范围查（trace/flows/taint 的锚点与递归步）：
	// ssa_value/parameter/field_access。其它 kind 的 func_id 语义不同——
	// external_summary 的 func_id 是"算出该摘要的调用方函数"（不是节点
	// 归属，ID 是外部函数 ID），不能套用前缀不变量。
	switch n.Kind {
	case domain.KindSSAValue, domain.KindParameter, domain.KindFieldAccess:
	default:
		return nil
	}
	raw, ok := n.Properties["func_id"]
	if !ok || raw == nil {
		return nil
	}
	var fid string
	switch v := raw.(type) {
	case string:
		fid = v
	case domain.CanonicalID:
		fid = string(v)
	default:
		fid = fmt.Sprint(v)
	}
	if fid == "" || strings.HasPrefix(string(n.ID), fid+"#") {
		return nil
	}
	return fmt.Errorf("node %s: func_id=%s 不满足 ID 前缀不变量（P1-11 前缀范围查依赖 funcID+\"#\" 前缀）", n.ID, fid)
}
