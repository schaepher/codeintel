package action

// todo20-9：调用点 → 被调符号映射。索引 calls 边按 (caller,target,kind)
// 汇总，重复调用只有首个位置带 pos/line metadata——byName 保存本函数
// 目标集合，按调用名/包路径/函数-方法类别唯一匹配后回退。

import (
	"go/ast"
	"go/token"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqTargets 时序图调用点 → 被调符号映射（R99-3——用户检查发现：
// 原 map[int]string 按行号，同一行多个调用时后写覆盖先写，两个
// 时序节点都拿到同一个 target）。byPos 按字节 offset（发射端
// metadata pos——同行各调用独立，精确匹配）；byLine 按行号
// （兼容无 pos 的旧索引——同行覆盖为旧行为，仅 fallback）。
type seqTargets struct {
	byPos  map[int]seqTarget
	byLine map[int]seqTarget
	// byName 本次函数调用的全部目标（按符号短名索引）——重复调用点的
	// 边被合并后靠它恢复后续调用点的目标身份（todo20-9）。
	byName map[string][]seqTarget
}

// seqTarget 调用点对应的被调符号 + 边置信度（todo10-3 证据用）。
type seqTarget struct {
	tid        string
	confidence float64
	// aggregated 目标来自同函数汇总边的名称回退（不是该位置的 pos
	// 直接命中）——证据只能标 corroborated（todo20-9）。
	aggregated bool
}

// lookup 查调用位置对应的被调符号：offset 优先，行号 fallback。
func (t *seqTargets) lookup(pos token.Position) (seqTarget, bool) {
	if t.byPos != nil {
		if tg, ok := t.byPos[pos.Offset]; ok {
			return tg, true
		}
	}
	if t.byLine != nil {
		if tg, ok := t.byLine[pos.Line]; ok {
			return tg, true
		}
	}
	return seqTarget{}, false
}

// lookupCall 位置查目标：byPos 精确命中直接用；byLine（旧索引无 pos）
// 必须核对调用名/包/种类，不能把同行的另一个包调用当成本调用；核对失败
// 回退按目标集合唯一匹配（todo23 §3.1）。
func (t *seqTargets) lookupCall(pos token.Position, fun ast.Expr, f *ast.File,
	curPkg, recvType, recvName string, recvDecl map[string]string) (seqTarget, bool) {
	if t.byPos != nil {
		if tg, ok := t.byPos[pos.Offset]; ok && seqTargetMatchesCall(tg, fun, f, curPkg, recvType, recvName, recvDecl) {
			return tg, true
		}
	}
	if t.byLine != nil {
		if tg, ok := t.byLine[pos.Line]; ok && seqTargetMatchesCall(tg, fun, f, curPkg, recvType, recvName, recvDecl) {
			return tg, true
		}
	}
	return t.matchAggregated(fun, f, curPkg, recvType, recvName, recvDecl)
}

// matchAggregated 位置未命中时按"本次函数调用的目标集合 + 调用名"回退
// （todo20-9）。索引 calls 边按 (caller,target,kind) 汇总，重复调用只有
// 首个位置的 metadata；不能因此把后续调用点判成 unresolved。
// 只有过滤后**唯一**候选才回退——同名多目标保持未解析，不猜。
func (t *seqTargets) matchAggregated(fun ast.Expr, f *ast.File, curPkg, recvType, recvName string, recvDecl map[string]string) (seqTarget, bool) {
	name, pkgHint, wantMethod, kindKnown := seqCallSiteIdentity(fun, f, curPkg, recvType, recvDecl)
	if name == "" {
		return seqTarget{}, false
	}
	var matched []seqTarget
	for _, tg := range t.byName[name] {
		pkg, _, method := seqSplitTarget(tg.tid)
		if pkgHint != "" && pkg != pkgHint {
			continue
		}
		if kindKnown && method != wantMethod {
			continue
		}
		matched = append(matched, tg)
	}
	// todo24 §11.6：多个同名方法候选时，按调用点**可证明**的 receiver 类型
	// 消歧；不可证明则保持歧义（不选第一个）。唯一候选沿用既有回退语义
	// （接口方法目标记录的是静态接口类型，与局部具体类型不同不算矛盾）。
	if len(matched) > 1 && wantMethod {
		matched = seqFilterByRecvType(matched, fun, recvType, recvName, recvDecl)
	}
	if len(matched) != 1 {
		return seqTarget{}, false
	}
	tg := matched[0]
	tg.aggregated = true
	return tg, true
}

// seqCallSiteIdentity 调用表达式的静态身份：(短名, 包路径提示, 是否方法,
// 是否已知函数/方法类别)。包提示只用于排除同名跨包目标；未知时留空。
func seqCallSiteIdentity(fun ast.Expr, f *ast.File, curPkg, recvType string, recvDecl map[string]string) (name, pkg string, method, kindKnown bool) {
	switch x := fun.(type) {
	case *ast.Ident:
		// 同包函数（含递归调用）
		return x.Name, curPkg, false, true
	case *ast.SelectorExpr:
		name = x.Sel.Name
		id, ok := x.X.(*ast.Ident)
		if !ok {
			// 嵌套 receiver（`box.worker.Error()`）：一定是方法/字段值调用，
			// 不能按同名借用独立包函数（todo24 §11.6）。
			return name, "", true, true
		}
		// 先核对 AST 局部对象：参数/局部变量遮蔽导入别名时（`logger := w;
		// logger.Error()`）它**不是**包调用，绝不能按导入路径当包函数。
		if id.Obj != nil && id.Obj.Kind != ast.Pkg {
			if recvType != "" && id.Name == recvType {
				return name, curPkg, true, true
			}
			return name, "", true, true
		}
		if path := seqImportPathOfName(f, id.Name); path != "" {
			return name, path, false, true
		}
		if recvType != "" && id.Name == recvType {
			return name, curPkg, true, true
		}
		if _, ok := recvDecl[id.Name]; ok {
			return name, "", true, true
		}
		return name, "", true, true
	}
	return "", "", false, false
}

// seqImportPathOfName 导入名（默认包名或显式别名）→ 导入路径。
func seqImportPathOfName(f *ast.File, name string) string {
	if f == nil || name == "" {
		return ""
	}
	for _, im := range f.Imports {
		path := strings.Trim(im.Path.Value, "\"")
		if path == "" {
			continue
		}
		local := path
		if i := strings.LastIndex(local, "/"); i >= 0 {
			local = local[i+1:]
		}
		if im.Name != nil {
			local = im.Name.Name
		}
		if local == name {
			return path
		}
	}
	return ""
}

// seqTargetsFor 构建某函数的"调用点 → 目标"映射（原始 calls 边）。
// todo22 §2：函数字面量内联展开时复用**字面量所在函数**的映射——索引把
// 闭包体内的调用记在外层函数上（位置偏移仍是全文件绝对偏移）。
func seqTargetsFor(a *Actions, req CodeSequenceRequest, symID string) *seqTargets {
	tgts := &seqTargets{}
	sqlStart := time.Now()
	// todo21-14.3：请求上下文下的调用边走查（一跳 = 直接邻接查询；取消要能
	// 终止实际 SQL）——阈值沿用 MinConfidence。
	facts, calleeErr := a.repo.GetCalleesContext(req.ctx(), domain.CanonicalID(symID), 1, MinConfidence)
	req.acc.sqlQueries++
	req.acc.sqlDur += time.Since(sqlStart)
	if calleeErr != nil {
		return tgts
	}
	for _, f := range facts {
		if ln, ok := f.Metadata["line_num"].(float64); ok {
			if tgts.byLine == nil {
				tgts.byLine = map[int]seqTarget{}
			}
			tgts.byLine[int(ln)] = seqTarget{tid: string(f.TargetID), confidence: f.Confidence}
		}
		if p, ok := f.Metadata["pos"].(float64); ok {
			if tgts.byPos == nil {
				tgts.byPos = map[int]seqTarget{}
			}
			tgts.byPos[int(p)] = seqTarget{tid: string(f.TargetID), confidence: f.Confidence}
		}
		// todo20-9：边按 (caller,target,kind) 汇总——重复调用只有首个位置的
		// metadata。保留目标集合供未命中位置按调用名回退。
		if name := seqTargetName(string(f.TargetID)); name != "" {
			if tgts.byName == nil {
				tgts.byName = map[string][]seqTarget{}
			}
			dup := false
			for _, e := range tgts.byName[name] {
				if e.tid == string(f.TargetID) {
					dup = true
					break
				}
			}
			if !dup {
				tgts.byName[name] = append(tgts.byName[name], seqTarget{tid: string(f.TargetID), confidence: f.Confidence})
			}
		}
	}
	return tgts
}

// seqASTCallTarget 按 AST 对象身份/导入路径恢复**准确**调用目标（不查名称、
// 不做模糊搜索）：包级函数的 Ident 声明对象，或包选择器 pkg.Fn 的 import
// 路径。todo22 §2.2/§3.1：实参位置的调用在索引里没有 calls 边（适配器只对
// 非实参调用建边），但工厂/回调实参必须能按准确符号展开。
func seqASTCallTarget(fun ast.Expr, f *ast.File, curPkg string) (string, bool) {
	switch fn := fun.(type) {
	case *ast.Ident:
		if fn.Obj == nil || fn.Obj.Kind != ast.Fun {
			return "", false
		}
		if curPkg == "" {
			return "", false
		}
		return "symbol:go:" + curPkg + ":" + fn.Name, true
	case *ast.SelectorExpr:
		id, ok := fn.X.(*ast.Ident)
		if !ok || (id.Obj != nil && id.Obj.Kind != ast.Pkg) {
			return "", false
		}
		if p := seqImportPath(f, id.Name); p != "" {
			return "symbol:go:" + p + ":" + fn.Sel.Name, true
		}
	}
	return "", false
}

// todo22 §3.2：接口候选展开预算——最多记录/展开 12 个实现候选。
const seqMaxIfaceCandidates = 12

// capSeqCandidates 截断候选集合（保留前 12 个），返回是否发生截断。
func capSeqCandidates(cands []string) ([]string, bool) {
	if len(cands) <= seqMaxIfaceCandidates {
		return cands, false
	}
	return cands[:seqMaxIfaceCandidates], true
}

// seqIfaceExpandBudget 候选**分支展开**的预算说明（T07：父列表保留全部
// 候选，展开前 N 个；每个候选最多 2 层）。
func seqIfaceExpandBudget(total int, expanded int, truncated bool) string {
	txt := "impls=" + itoaSeq(total) + "（展开前 " + itoaSeq(expanded) + " 个候选，候选深度 2）"
	if truncated {
		txt = "impls>" + itoaSeq(seqMaxIfaceCandidates) + "（已截断为 " + itoaSeq(expanded) +
			"，展开前 " + itoaSeq(expanded) + " 个候选，候选深度 2）"
	}
	return txt
}

// seqIfaceBudget 候选预算的说明文本（截断/未展开原因，消费者可核对）。
func seqIfaceBudget(n int, truncated bool) string {
	if truncated {
		return "impls>" + itoaSeq(seqMaxIfaceCandidates) + "（已截断为 " +
			itoaSeq(seqMaxIfaceCandidates) + "，候选深度 0=未展开）"
	}
	return "impls=" + itoaSeq(n) + "（候选深度 0=未展开）"
}
