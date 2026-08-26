// Package action 是 CLI 与 HTTP 共享的查询用例层（应用层）：
// 命令行和 HTTP 接口本身只负责参数解析与结果展示，全部图查询
// 经此层调用仓储。依赖方向：action → Reader 窄接口 ← *sqlite.Repo。
package action

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// MinConfidence 调用关系查询默认置信度阈值（业务规则，TD.md 5.1：
// CALLS 边置信度 0.8；0.85 会过滤掉全部调用边）。
const MinConfidence = 0.8

// Reader 是 action 层依赖的仓储窄接口（*sqlite.Repo 实现）。
type Reader interface {
	GetSymbol(id domain.CanonicalID) (*domain.CodeEntity, error)
	GetSymbolByName(name string) ([]*domain.CodeEntity, error)
	SearchKind(name, kind string) ([]*domain.CodeEntity, error) // #234 搜索按类型过滤（kind 空=不过滤）
	GetCallers(id domain.CanonicalID, depth int, minConfidence float64) ([]*domain.Fact, error)
	GetCallees(id domain.CanonicalID, depth int, minConfidence float64) ([]*domain.Fact, error)
	GetImpact(id domain.CanonicalID, depth int) ([]*domain.CodeEntity, error)
	// InterfaceMethodImpl R75：接口方法 → 实现方法（implements 边 +
	// 方法名匹配——调用链接口具体化）
	InterfaceMethodImpl(methodID string) (string, bool)
	GetFunctionFields(funcID domain.CanonicalID) ([]*domain.FunctionFieldSummary, error)
	TraceBackward(field string, funcID domain.CanonicalID, maxDepth int) ([]*domain.TraceRow, error)
	TraceBackwardIndirect(field string, funcID domain.CanonicalID, maxDepth int) ([]*domain.TraceRow, error) // Q172 --follow-indirect
	TraceForward(field string, funcID domain.CanonicalID, maxDepth int) ([]*domain.TraceRow, error)
	GetValueTrace(nodeID domain.CanonicalID, maxDepth int, minConf float64, includeContainer bool) ([]*domain.TraceRow, error) // Q161/Q163
	GetValueTraceMulti(anchors []domain.CanonicalID, ctxField string, maxDepth int) ([]*domain.TraceRow, error)
	GetFunctionFlows(funcID domain.CanonicalID, maxDepth int) ([]*domain.TraceRow, error)
	GetRoots() ([]*domain.CodeEntity, error)
	GetPackages() ([]*domain.CodeEntity, error) // R1：包职责地图（doc_comment）
	Expand(id domain.CanonicalID) (facts []*domain.Fact, nodes []*domain.CodeEntity, err error)
	AllSummaries() ([]*domain.FunctionFieldSummary, error)
	GetIndirectWriteEdges(funcID domain.CanonicalID) ([]*domain.Fact, error)
	GetDispatchEdges(ifaceID domain.CanonicalID) ([]*domain.Fact, error)
	GetDispatchTargets() (map[domain.CanonicalID]domain.DispatchMeta, error) // Q157 P1
	FindFieldReads(fullPath string) ([]*domain.CodeEntity, error)
	GetTableColumns(table string) ([]*domain.TableColumn, error)
	GetAllTableColumns() ([]*domain.TableColumn, error) // ER 图：全库外部列（无 writers/readers 明细）
	GetTableRelations(table, memoryMode string) ([]*domain.TableRelation, error)
	GetAllTableRelations(memoryMode string) ([]*domain.TableRelation, error)   // Q160 全库聚合
	GetTables() ([]string, error)                                              // Q241 表名枚举（table-path 表名解析）
	SymbolsAt(file string, line int) ([]*domain.CodeEntity, error)             // #229 file:line 定位符号
	RecentChanges(limit int) ([]*domain.RecentChange, error)                   // #237 最近变更
	TopCallersInModule(prefix string, limit int) ([]*domain.WikiSymbol, error) // #238 wiki 核心符号
	GetAllCalls() ([]*domain.Fact, error)                                      // Q251-A wiki 包间调用图聚合
	TablesWrittenByModule(prefix string) ([]string, error)                     // #238 wiki 相关表
	TopLevelEntries() ([]*domain.CodeEntity, error)                            // #238 wiki 入口（main+服务，不含框架回调）
	GetEntityRaw() (*domain.EntityRaw, error)                                  // R9 实体协作图原始数据（类型/函数/has_method/calls）
	GetTableSchemas() (map[string]string, error)                               // R19 表 schema 事实源（列类型/默认值）
	GetFunctions() ([]*domain.CodeEntity, error)                               // R89 helpers：游离函数清单（kind=function 非方法）
	GetUncalledFunctions() ([]*domain.UnusedFunc, error)
	GetIsolatedChains() ([][]*domain.UnusedFunc, error)
	GetPath(from, to domain.CanonicalID, maxDepth int, viaCalls bool) ([]*domain.TraceRow, error)
	GetGrpcCalls() ([]*domain.GrpcCallRow, error)
	// R92：grpc/http 路由清单（query grpc-routes/http-routes）
	GetGrpcServices() ([]*domain.CodeEntity, error)                   // kind=grpc_service（含 properties）
	GetRegisterNode(svcName string) (*domain.CodeEntity, error)       // registers_service 属性
	GetFirstCallTo(targetID domain.CanonicalID) (*domain.Fact, error) // 首条 calls 入边（含行号）
	GetGrpcImplNode(svcID domain.CanonicalID) (*domain.CodeEntity, error)
	GetImplementsTarget(ifaceID domain.CanonicalID) (domain.CanonicalID, error) // implements 边（排除 Unimplemented 桩）
	GetHTTPRouteNodes() ([]*domain.CodeEntity, error)                           // kind=http_route（含 properties）
	// R94：外部依赖（redis/kafka）与外部接口调用（query external-*）
	GetRedisKeyNodes() ([]*domain.CodeEntity, error)         // kind=redis_key（properties.write/cmd）
	GetKafkaTopicNodes() ([]*domain.CodeEntity, error)       // kind=kafka_topic
	GetFactsByKinds(kinds ...string) ([]*domain.Fact, error) // 指定 kind 的调用边（metadata 全量）
	Counts() (nodes int, edges int, err error)
	GetLatest() (*domain.BuildMeta, error)
	RepoPath() string

	// Q226：用户连线规则（CLI rule / ER 页面配置）——写操作 + 列表
	AddRelationRule(rule domain.RelationRule) (int64, error)
	ListRelationRules() ([]domain.RelationRule, error)
	RemoveRelationRule(id int64) error

	// R95：grpc/http 调用链（query grpc-callers/http-callers/ext-chain）
	GetImplementsEdges() ([]*domain.Fact, error) // 全部 implements 边（排除 Unimplemented 桩）
	AllNodeIDs() ([]domain.CanonicalID, error)   // 全部节点 ID（接口具体化判定）
	EnsureExtChainCache() error                  // 建 ext_chain_cache 表（幂等）
	ExtChainCacheGet(symbol, build string) (string, bool)
	ExtChainCacheSet(symbol, build, result string) error

	// Q228：全量 relations 计算进度（precompute 命令 / serve 后台任务）
	RelationProgress() (domain.RelationProgress, error)
	StartRelationComputeIfNeeded() (bool, error)
	PrecomputeAllRelations(progressFn func(done, total int)) error
	AllSymbolNames(limit int) ([]string, error) // Q244 相似名候选池
}

// Actions 是 CLI 与 HTTP 共享的查询用例集合。
type Actions struct {
	repo     Reader
	modNames []string // 全部 module 路径缓存（P2-3 多 go.mod；modules() 填充）
}

// AddRelationRule 添加用户连线规则（Q226，薄封装）。
func (a *Actions) AddRelationRule(rule domain.RelationRule) (int64, error) {
	logger := zap.L()
	logger.Info("enter (Actions).AddRelationRule")
	defer logger.Info("exit (Actions).AddRelationRule")
	return a.repo.AddRelationRule(rule)
}

// ListRelationRules 列出用户连线规则（Q226，薄封装）。
func (a *Actions) ListRelationRules() ([]domain.RelationRule, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ListRelationRules")
	defer logger.Info("exit (Actions).ListRelationRules")
	return a.repo.ListRelationRules()
}

// RemoveRelationRule 删除用户连线规则（Q226，薄封装）。
func (a *Actions) RemoveRelationRule(id int64) error {
	logger := zap.L()
	logger.Info("enter (Actions).RemoveRelationRule")
	defer logger.Info("exit (Actions).RemoveRelationRule")
	return a.repo.RemoveRelationRule(id)
}

// RelationProgress 全量 relations 计算进度（Q228，薄封装）。
func (a *Actions) RelationProgress() (domain.RelationProgress, error) {
	logger := zap.L()
	logger.Info("enter (Actions).RelationProgress")
	defer logger.Info("exit (Actions).RelationProgress")
	return a.repo.RelationProgress()
}

// StartRelationComputeIfNeeded 查询端自动兜底启动计算（Q228，薄封装）：
// 返回 started=true 表示调用方应起 goroutine 执行 PrecomputeAllRelations。
func (a *Actions) StartRelationComputeIfNeeded() (bool, error) {
	logger := zap.L()
	logger.Info("enter (Actions).StartRelationComputeIfNeeded")
	defer logger.Info("exit (Actions).StartRelationComputeIfNeeded")
	return a.repo.StartRelationComputeIfNeeded()
}

// PrecomputeAllRelations 全量计算并写缓存（Q228，薄封装）。
func (a *Actions) PrecomputeAllRelations(progressFn func(done, total int)) error {
	logger := zap.L()
	logger.Info("enter (Actions).PrecomputeAllRelations")
	defer logger.Info("exit (Actions).PrecomputeAllRelations")
	return a.repo.PrecomputeAllRelations(progressFn)
}

// New 创建 Actions。
func New(repo Reader) *Actions {
	return &Actions{repo: repo}
}

// 写锚点跳板限界（④ 超时防护）：读节点数上限 + 子追溯深度——避免
// 同字段大量读节点各自跑一遍深度 8 双向全链。
const (
	trampolineMaxReads = 8
	trampolineDepth    = 4
)

// TraceParams 字段追溯参数（S2/S3）。
type TraceParams struct {
	Field          string
	Func           string // 函数符号输入（canonical ID 或名称）
	MaxDepth       int
	Forward        bool // true=trace-forward（S3 后续使用），false=trace-backward（S2 产生点）
	FollowIndirect bool // Q172：trace-backward --follow-indirect（跨函数间接写链）
}

// markDispatchCandidates 标注候选派发（Q157 P1）：value-trace 行所属
// 函数是接口 dispatch_to 边 target（候选实现）时标记来源与置信度——
// 链路混入多个接口候选实现时可区分。

func (a *Actions) markDispatchCandidates(rows []*domain.TraceRow) ([]*domain.TraceRow, error) {
	targets, err := a.repo.GetDispatchTargets()
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return rows, nil
	}
	for _, r := range rows {
		if r.FuncID == "" {
			continue
		}
		if m, ok := targets[domain.CanonicalID(r.FuncID)]; ok {
			r.DispatchCandidate = true
			r.DispatchOrigin = m.Origin
			r.DispatchConf = m.Confidence
		}
	}
	return rows, nil
}

// Roots 返回顶层入口节点（前端初始视图）。
func (a *Actions) Roots() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Roots")
	defer logger.Info("exit (Actions).Roots")
	return a.repo.GetRoots()
}

// Search 全库符号搜索（名称/ID 模糊匹配，上限由仓储实现决定；#234
// kind 非空时按类型过滤——页面搜索类型选择）。
func (a *Actions) Search(q, kind string) ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Search", zap.String("q", q), zap.String("kind", kind))
	defer logger.Info("exit (Actions).Search")
	if kind == "" {
		return a.repo.GetSymbolByName(q)
	}
	return a.repo.SearchKind(q, kind)
}

// Expand 返回节点的直接邻居（facts + 邻居节点）；返回当前节点供存在性检查。
func (a *Actions) Expand(id domain.CanonicalID) (cur *domain.CodeEntity, facts []*domain.Fact, nodes []*domain.CodeEntity, err error) {
	logger := zap.L()
	logger.Info("enter (Actions).Expand", zap.String("id", string(id)))
	defer logger.Info("exit (Actions).Expand")
	cur, err = a.repo.GetSymbol(id)
	if err != nil {
		return nil, nil, nil, err
	}
	facts, nodes, err = a.repo.Expand(id)
	if err != nil {
		return nil, nil, nil, err
	}
	return cur, facts, nodes, nil
}
