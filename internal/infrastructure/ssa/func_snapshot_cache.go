package ssa

// 构建级只读缓存（Q?）：挂在 funcSnapshot（Index 级共享）上，避免同一
// 函数/类型/SQL 在不同 extractor（按包并发）里重复计算。所有缓存只增
// 不改，空结果也缓存；并发访问加锁。生命周期与 funcSnapshot 一致，
// 不跨 SSA Program（文档要求：编号/身份缓存不得跨 Program 复用）。

import (
	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/schaepher/codeintel/internal/domain"
)

// funcIdent 函数身份三元组（canonical ID / kind / name）。
type funcIdent struct {
	id   domain.CanonicalID
	kind domain.EntityKind
	name string
}

// identityOf 提取器侧函数身份：快照存在走构建级缓存；快照缺失（如
// emitGlobalInit 的轻量提取器）回退纯 funcIdentity——避免 ext.funcs 为 nil
// 时静默返回空 ID 而丢节点（Q252f 静默退化教训）。
func (ext *fieldExtractor) identityOf(fn *types.Func) funcIdent {
	if ext == nil || ext.funcs == nil {
		id, kind, name := funcIdentity(fn)
		return funcIdent{id: id, kind: kind, name: name}
	}
	return ext.funcs.identityOf(fn)
}

// identityOf 函数身份（funcIdentity 的构建级缓存，含失败结果——失败时
// id 为空，同样缓存，避免同函数重复解析）。
func (s *funcSnapshot) identityOf(fn *types.Func) funcIdent {
	if s == nil || fn == nil {
		return funcIdent{}
	}
	s.identMu.Lock()
	if v, ok := s.idents[fn]; ok {
		s.identMu.Unlock()
		return v
	}
	s.identMu.Unlock()
	id, kind, name := funcIdentity(fn)
	v := funcIdent{id: id, kind: kind, name: name}
	s.identMu.Lock()
	if s.idents == nil {
		s.idents = map[*types.Func]funcIdent{}
	}
	s.idents[fn] = v
	s.identMu.Unlock()
	return v
}

// staticCallSites 静态调用点反向索引（被调函数 → 每个静态调用点的实参
// 列表）。惰性构建一次：遍历函数全集 × 基本块 × 指令，与旧实现的扫描
// **顺序完全一致**（函数列表 → 块 → 指令），因此候选顺序不变。用于
// resolveParamCandidates（原先对每个目标函数全程序重扫）。
func (s *funcSnapshot) staticCallSites() map[*ssa.Function][][]ssa.Value {
	if s == nil {
		return nil
	}
	s.callSitesOnce.Do(func() {
		idx := map[*ssa.Function][][]ssa.Value{}
		for _, f := range s.list {
			for _, b := range f.Blocks {
				for _, instr := range b.Instrs {
					call, ok := instr.(*ssa.Call)
					if !ok {
						continue
					}
					callee := call.Call.StaticCallee()
					if callee == nil {
						continue
					}
					idx[callee] = append(idx[callee], call.Call.Args)
				}
			}
		}
		s.callSites = idx
	})
	return s.callSites
}

// returnOperandsOf 被调函数 Return 指令操作数（构建级缓存，空结果也缓存）。
func (s *funcSnapshot) returnOperandsOf(fn *ssa.Function) [][]ssa.Value {
	if s == nil || fn == nil {
		return nil
	}
	s.retMu.Lock()
	if v, ok := s.rets[fn]; ok {
		s.retMu.Unlock()
		return v
	}
	s.retMu.Unlock()
	v := returnOperands(fn)
	s.retMu.Lock()
	if s.rets == nil {
		s.rets = map[*ssa.Function][][]ssa.Value{}
	}
	s.rets[fn] = v
	s.retMu.Unlock()
	return v
}

// tableNameCached 类型表名缓存（compute 只在未命中时调用；空串同样缓存
// ——该类型无 TableName）。保持 tableNameOf 的优先级语义（调用方传入的
// compute 即 tableNameOfSlow）。
func (s *funcSnapshot) tableNameCached(named *types.Named, compute func() string) string {
	if s == nil || named == nil {
		return compute()
	}
	s.tableMu.Lock()
	if v, ok := s.tableNames[named]; ok {
		s.tableMu.Unlock()
		return v
	}
	s.tableMu.Unlock()
	v := compute()
	s.tableMu.Lock()
	if s.tableNames == nil {
		s.tableNames = map[*types.Named]string{}
	}
	s.tableNames[named] = v
	s.tableMu.Unlock()
	return v
}

// sqlParseEntry SQL 解析缓存条目（含解析路径 outcome——命中时据此保持
// 降级统计口径：按**调用**计数而非按唯一 SQL；classifyHeuristic 已去重，
// 命中时不重复分类）。
type sqlParseEntry struct {
	table     string
	alias     string
	cols      []string
	whereCols []string
	joinPairs []sqlJoinPair
	outcome   int
}

// SQL 解析路径 outcome（与降级统计对应）。
const (
	sqlOutcomeNoCount   = 0 // SQLite 特有命令（VACUUM/PRAGMA...）——不占降级计数
	sqlOutcomeAST       = 1 // AST 主路径成功
	sqlOutcomeHeuristic = 2 // AST 失败→启发式降级
)

// sqlCacheLimit SQL 解析缓存条数上限（超出后不再新增——限制内存）。
const sqlCacheLimit = 8192

// sqlParseCached 查 SQL 解析缓存；未命中返回 (entry, false)。
func (s *funcSnapshot) sqlParseCached(sql string) (sqlParseEntry, bool) {
	if s == nil {
		return sqlParseEntry{}, false
	}
	s.sqlMu.Lock()
	defer s.sqlMu.Unlock()
	if e, ok := s.sqlCache[sql]; ok {
		return e, true
	}
	return sqlParseEntry{}, false
}

// storeSQLParse 写 SQL 解析缓存（达上限后不再新增）。
func (s *funcSnapshot) storeSQLParse(sql string, e sqlParseEntry) {
	if s == nil || s.sqlCacheFull {
		return
	}
	s.sqlMu.Lock()
	defer s.sqlMu.Unlock()
	if s.sqlCache == nil {
		s.sqlCache = map[string]sqlParseEntry{}
	}
	if len(s.sqlCache) >= sqlCacheLimit {
		s.sqlCacheFull = true
		return
	}
	s.sqlCache[sql] = e
}
