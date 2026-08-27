package action

// R100 待办9：wiki 数据源全部改 action——残余直连 sqlite（archSvcPkgs
// 裸 SQL / GetPkgCodeFacts / QAForSymbols）与源码解析（ORM 结构体扫描 /
// internal/server 路由解析）迁 action；wiki 只组合 action 结果到
// html/md。

import (
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ArchSvcPkgs 接入层服务包集合（grpc_service/http_route 节点所在包短名
// ——R82：识别到 grpc/http 服务就归接入层，不依赖包短名约定）。原
// archSvcPkgs 裸 SQL 收口（组合 GetGrpcServices + GetHTTPRouteNodes）。
func (a *Actions) ArchSvcPkgs() ([]string, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ArchSvcPkgs")
	defer logger.Info("exit (Actions).ArchSvcPkgs")
	set := map[string]bool{}
	add := func(nodes []*domain.CodeEntity) {
		for _, n := range nodes {
			p := SymbolPkg(string(n.ID))
			if i := strings.LastIndex(p, "/"); i >= 0 {
				p = p[i+1:]
			}
			if p != "" {
				set[p] = true
			}
		}
	}
	svcs, err := a.repo.GetGrpcServices()
	if err != nil {
		return nil, err
	}
	add(svcs)
	routes, err := a.repo.GetHTTPRouteNodes()
	if err != nil {
		return nil, err
	}
	add(routes)
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// PkgCodeFacts 包内代码事实（fallback——无包级说明时展示结构体/方法/
// 函数签名；R9x 仓储层 GetPkgCodeFacts 收口）。
func (a *Actions) PkgCodeFacts(pkgPath string) (*domain.PkgCodeFacts, error) {
	logger := zap.L()
	logger.Info("enter (Actions).PkgCodeFacts", zap.String("pkg", pkgPath))
	defer logger.Info("exit (Actions).PkgCodeFacts")
	return a.repo.GetPkgCodeFacts(pkgPath)
}

// QAReferences 历史问答参考资料（--with-qa）：按关键词匹配 qa_history
// （context/question LIKE），限量 limit 条。
func (a *Actions) QAReferences(keywords []string, limit int) ([]*domain.QARecord, error) {
	logger := zap.L()
	logger.Info("enter (Actions).QAReferences", zap.Int("kw", len(keywords)))
	defer logger.Info("exit (Actions).QAReferences")
	return a.repo.QAForSymbols(keywords, limit)
}
