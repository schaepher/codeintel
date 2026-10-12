package action

// 调用者反向具体化（从 action_query.go 拆出——行数治理 300 行上限）：
// 查询实现方法的调用者时，把"经接口方法发出的调用"映射回该实现方法。

import (
	"fmt"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// CallersConcrete 调用者 + 接口反向具体化（查询实现方法的调用者时，
// 反查其实现的接口方法，再把调用接口方法的首跳映射回该实现方法——
// 接口跳板不占层数；解决"经接口发出的调用在实现方法的 caller 列表里
// 看不到"的断链，与 CalleesConcrete 对称）。只处理"接口方法 → 实现方
// 法"的精确映射；索引只记录为接口类型而未记录方法名的形态不猜测
// （避免把调用错误归因到某个实现方法）。
func (a *Actions) CallersConcrete(id domain.CanonicalID, depth int) ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Info("enter (Actions).CallersConcrete", zap.String("id", string(id)), zap.Int("depth", depth))
	defer logger.Info("exit (Actions).CallersConcrete")
	facts, err := a.repo.GetCallers(id, depth, MinConfidence)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(facts))
	for _, f := range facts {
		seen[factKey(f)] = true
	}
	for _, im := range a.repo.ConcreteMethodIfaces(string(id)) {
		sub, err := a.repo.GetCallers(domain.CanonicalID(im), 1, MinConfidence)
		if err != nil {
			continue
		}
		for _, f := range sub {
			nf := *f
			nf.TargetID = id
			md := make(map[string]any, len(f.Metadata)+1)
			for k, v := range f.Metadata {
				md[k] = v
			}
			md["via_iface"] = im
			nf.Metadata = md
			k := factKey(&nf)
			if seen[k] {
				continue
			}
			seen[k] = true
			facts = append(facts, &nf)
		}
	}
	return facts, nil
}

// factKey 边的去重键（调用者反向具体化时避免与已存在边重复）。
func factKey(f *domain.Fact) string {
	return fmt.Sprintf("%s|%s|%s|%v", f.SourceID, f.TargetID, f.Kind, f.Metadata["line_num"])
}
