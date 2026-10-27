package action

import (
	"errors"
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ResolveSymbol 将用户输入解析为符号：canonical ID 直接命中，否则按名称查找；
// 多匹配时返回错误并列出候选 ID（原 CLI 语义，供符号类 action 复用）。
func (a *Actions) ResolveSymbol(input string) (*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ResolveSymbol", zap.String("input", input))
	defer logger.Info("exit (Actions).ResolveSymbol")
	if strings.HasPrefix(input, "symbol:") || strings.HasPrefix(input, "file:") || strings.HasPrefix(input, "commit:") {
		n, err := a.repo.GetSymbol(domain.CanonicalID(input))
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	matches, err := a.repo.GetSymbolByName(input)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		// Q244：相似名提示（前缀/编辑距离 ≤2）
		if names, err := a.repo.AllSymbolNames(5000); err == nil {
			if cands := similarCandidates(input, names, 5); len(cands) > 0 {
				return nil, fmt.Errorf("符号 %q 不存在，你是要找 %s？", input, strings.Join(cands, " / "))
			}
		}
		return nil, fmt.Errorf("符号 %q 不存在", input)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("符号 %q 有 %d 个匹配，请使用 canonical ID:\n  %s",
			input, len(matches), joinIDs(matches))
	}
	return matches[0], nil
}

// joinIDs 拼接候选 ID（多匹配错误提示用）。
func joinIDs(nodes []*domain.CodeEntity) string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, string(n.ID))
	}
	return strings.Join(ids, "\n  ")
}

// ResolveAnchor 解析摘要/生命周期锚点（③⑤）：canonical ID 直连、符号
// 名称解析，类型限定字段路径（example.com/m.T.A）回退到同字段读节点
// （FindFieldReads 首个）——此前字段路径被误报"不存在的符号"。
func (a *Actions) ResolveAnchor(input string) (domain.CanonicalID, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ResolveAnchor", zap.String("input", input))
	defer logger.Info("exit (Actions).ResolveAnchor")
	if strings.HasPrefix(input, "symbol:") || strings.HasPrefix(input, "file:") || strings.HasPrefix(input, "commit:") {
		if _, err := a.repo.GetSymbol(domain.CanonicalID(input)); err == nil {
			return domain.CanonicalID(input), nil
		}
	}
	if n, err := a.ResolveSymbol(input); err == nil {
		return n.ID, nil
	}
	if reads, err := a.repo.FindFieldReads(input); err == nil && len(reads) > 0 {
		return reads[0].ID, nil
	}
	return "", fmt.Errorf("符号或字段路径 %q 不存在", input)
}

// Symbol 按 canonical ID 查询符号（HTTP expand 的存在性检查用）。
func (a *Actions) Symbol(id domain.CanonicalID) (*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Symbol", zap.String("id", string(id)))
	defer logger.Info("exit (Actions).Symbol")
	return a.repo.GetSymbol(id)
}

// SymbolsByIDs 批量按 canonical ID 取节点（callers/callees JSON 富化
// 用），返回 id → 节点映射（未命中的 id 无对应项）。
func (a *Actions) SymbolsByIDs(ids []domain.CanonicalID) (map[domain.CanonicalID]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Info("enter (Actions).SymbolsByIDs", zap.Int("n", len(ids)))
	defer logger.Info("exit (Actions).SymbolsByIDs")
	nodes, err := a.repo.GetSymbolsByIDs(ids)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.CanonicalID]*domain.CodeEntity, len(nodes))
	for _, n := range nodes {
		out[n.ID] = n
	}
	return out, nil
}

// Entries 目标仓库入口符号（name=main 的函数，排除测试）——wiki
// 命令页数据源（F1：命令页展示目标仓库入口而非 codeintel 自身命令）。
func (a *Actions) Entries() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Entries")
	defer logger.Info("exit (Actions).Entries")
	nodes, err := a.repo.GetSymbolByName("main")
	if err != nil {
		return nil, err
	}
	var out []*domain.CodeEntity
	for _, n := range nodes {
		if n.Kind != domain.KindFunction || strings.Contains(n.FilePath, "_test.go") {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

// DispatchCandidate 接口派发候选实现（Q95：候选 + 置信度 + 注册点）。
//
// P1-03：这份组合口径属业务逻辑，统一由 SymbolDetail 提供——CLI/MCP/
// ask 都是消费者，不该各自拼装（原 cli 独有，MCP 承诺 candidates 却缺）。
type DispatchCandidate struct {
	Impl         domain.CanonicalID // 候选实现（方法节点 ID）
	Method       string             // 接口方法名（metadata interface_method）
	Origin       string             // register | enum | guess（Q93 置信档）
	Confidence   float64            // 0.9 / 0.7 / 0.5
	RegisterSite int                // 注册行号（0=无注册点）
	RegisterFile string             // 注册所在文件（空=未知）
}

// SymbolDetail 符号详情：基本信息 + 调用者/被调用者摘要（query symbol）。
type SymbolDetail struct {
	Node     *domain.CodeEntity
	Callers  []*domain.Fact
	Callees  []*domain.Fact
	Dispatch []DispatchCandidate // 接口类型 → 候选实现（非接口为空）
}

// dispatchCandidates 接口的派发候选（Q95）——由 SymbolDetail 统一组合。
func (a *Actions) dispatchCandidates(ifaceID domain.CanonicalID) []DispatchCandidate {
	facts, err := a.DispatchCandidates(ifaceID)
	if err != nil {
		return nil
	}
	out := make([]DispatchCandidate, 0, len(facts))
	for _, f := range facts {
		conf := metaFloat(f.Metadata, "confidence")
		if conf == 0 {
			conf = f.Confidence
		}
		out = append(out, DispatchCandidate{
			Impl:         f.TargetID,
			Method:       metaStr(f.Metadata, "interface_method"),
			Origin:       metaStr(f.Metadata, "origin"),
			Confidence:   conf,
			RegisterSite: metaInt(f.Metadata, "register_site"),
			RegisterFile: metaStr(f.Metadata, "register_file"),
		})
	}
	return out
}

// metaFloat metadata 数值（JSON 反序列化 float64；int 兜底）。
func metaFloat(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

// SymbolDetail 解析符号并返回其详情（调用者/被调用者深度 1）。
func (a *Actions) SymbolDetail(input string) (*SymbolDetail, error) {
	logger := zap.L()
	logger.Info("enter (Actions).SymbolDetail", zap.String("input", input))
	defer logger.Info("exit (Actions).SymbolDetail")
	n, err := a.ResolveSymbol(input)
	if err != nil {
		return nil, err
	}
	callers, err := a.CallersConcrete(n.ID, 1)
	if err != nil {
		return nil, err
	}
	callees, err := a.repo.GetCallees(n.ID, 1, MinConfidence)
	if err != nil {
		return nil, err
	}
	d := &SymbolDetail{Node: n, Callers: callers, Callees: callees}
	// 接口类型才查候选实现（Q95）；其余符号不产生额外查询。
	if n.Kind == domain.KindInterface {
		d.Dispatch = a.dispatchCandidates(n.ID)
	}
	return d, nil
}
