package action

import (
	"context"
	"errors"
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// 调用执行语义（todo10-2）：sync 缺省；defer 函数返回时执行；async 并发
// 启动（完成顺序未知，不得渲染成同步完成）。
const (
	SeqExecDefer = "defer"
	SeqExecAsync = "async"
)

// 终止原因（todo10-9；todo10-3 起扩展为按键诊断集）。
const (
	SeqStopNone     = "none"     // 自然结束（本函数语句走完）
	SeqStopDepth    = "depth"    // 深度上限：仍有可展开的调用点未展开
	SeqStopCanceled = "canceled" // 调用方取消
)

// ErrSeqCanceled 时序展开被取消（ctx 取消）——调用方不得当作成功。
var ErrSeqCanceled = errors.New("时序展开已取消")

// CodeSequenceRequest query sequence --code 参数。
type CodeSequenceRequest struct {
	Target       string    // 符号名或 canonical ID
	RepoAbs      string    // 仓库绝对路径（源码读取）
	Depth        int       // 嵌套层级（1 = 只本函数；>1 递归展开被调函数内部）
	StopPackages []string  // 停止包列表（cli 从 config.yaml 读取——命中不深入）
	Filter       SeqFilter // S5：导出过滤（按包/文件/函数/正则——命中不生成节点，如 log 调用）
	// Focus 关注目标（todo10-2 part3）：字段路径（pkg.T.F / T.F / F 后缀匹配）
	// 或 "return"（只看返回与返回表达式内调用）。空 = 不过滤。
	Focus []string
	// Scene 场景条件（todo10-5）：静态可判定的键值，用于分支裁剪与依据标注
	Scene Scene
	// Ctx 取消信号（todo10-9）：递归展开每语句检查一次；nil = 不取消
	Ctx context.Context
	// todo22 §4.1：RPC 跨仓续接（与 ext-chain 同款候选范围——cli 按 workspace
	// 限定；nil Open = 只看起始仓库）。
	CandidateRepos []string
	Open           SeqRepoOpener
	Scope          string // workspace:<dir> | all-registered | registered（证据用）
	ScopeReason    string
	// indexSHA 当前索引 commit（todo12-14：证据可复现性——内部填充）
	indexSHA string
	// entryID 入口 canonical ID（证据命令重放整棵分析树，而不是只查被调节点）
	entryID string
	// 内部：递归展开路径防环（R97——grpc 实现内部调接口、接口具体化
	// 回到同一 grpc 实现时自环；路径语义：进入标记、退出清除，不同
	// 分支互不影响）
	visited map[string]bool
	// callbackCtx todo23 §2.5：当前展开路径的**入站回调上下文**（函数值
	// 词法身份/绑定站点）——防环键包含它，避免"同一函数换个回调路径就
	// 被误判为循环"，也不能只用 executor 符号 ID。
	callbackCtx string
	// 内部：阶段耗时累加（todo10-9）
	acc *seqTimingAcc
	// 内部（todo10-2）：当前函数的关注行（字段写入点）+ 具名结果槽 + 焦点配置
	focusLines  map[int]bool
	resultNames []string
	focus       *seqFocus
	// 内部（todo10-5/11-2）：条件传播——调用点实参字面量（按位置）与形参映射
	callArgLits []string
	callArgSrcs []string // todo12-3：实参值来源（定义位置/传播依据）
	callArgWhy  []string // todo13-3：实参未知原因（call-may-write / depth-limit）
	// callArgFields todo22 P1：实参的结构体字段已知值（按位置；键为字段名）
	// ——被调函数形参的 `param.Field` 条件可据此裁剪。
	callArgFields []map[string]sceneArg
	// 内部（todo11-2）：当前函数 canonical ID / 文件（值流解析向上找调用点用）
	incomingArgs []string
	incomingSrc  []string // todo12-3：入站实参来源
	curBody      *ast.BlockStmt
	curParams    *ast.FieldList
	curFile      string
	sceneArgs    map[string]string
	sceneSrc     map[string]string // todo12-3：形参 → 值来源（分支判定证据）
	sceneWhy     map[string]string // todo13-3：形参 → 未知原因（partial 诊断）
	// todo22 §5：当前包（同目录）常量表 name → 值文本（整型/有类型常量参与
	// 场景判定；不把枚举名称直接当值）。todo22 P1：跨包常量
	// （otherpkg.Const）按 import 路径按需解析（请求内缓存）。
	sceneConsts *seqConstTable
	// constPkgs 跨包常量表（import path → name→值；nil = 未解析/不可解析）
	constPkgs map[string]map[string]string
	// diRegsAll todo22 P1：当前包 + 跨包引导函数的 DI 注册证据（请求内缓存；
	// 外层键为哨兵，值为 key→注册列表）
	diRegsAll map[string]map[string][]seqDIReg
	// todo22 P2 §7.3：receiver 字段类型/工厂来源的请求内缓存（同一字段的
	// 多次调用不重复查字段写入者/不重复解析写入函数源码）。
	fieldWriters map[string][]string
	fieldTypes   map[string][]writeSource // key: writerID\x00field
	funcRets     map[string][]writeSource // key: pkg\x00func（跨包构造器返回类型）
	varChains    map[string][]writeSource // key: file\x00var（变量链回溯）
	// todo22 §7.3：请求内接口实现候选缓存（ifaceID → 实现类型列表；nil =
	// 明确未命中，同样缓存，避免同一请求重复查库/重复全库发现）
	ifaceImpls map[string]seqIfaceCached
	// ifaceImplTypes todo23 §7.1：接口 → 实现**类型**（与所选方法无关）——
	// 方法级 ID 在读取时按当前方法重建，避免"先解析 Insert 再解析
	// SaveService 复用 Insert 的 ID"。
	ifaceImplTypes map[string][]string
	// todo22 §4.2：外部包源码解析缓存（import path → 包目录/文件；失败也缓存）
	extPkgs map[string]*seqExternalPkg
	// todo22 §4.1：RPC 转发/服务端解析缓存（客户端桩符号 → 转发；
	// 服务身份 → 注册/实现；未命中同样缓存——正常未命中不反复全库发现）
	rpcFwd  map[string]*domain.RPCForward
	rpcSvcs map[string]*seqRPCSvc
	// rpcByFunc 源函数 → RPC 出站边（调用点 pos/行号匹配；请求内缓存）
	rpcByFunc map[string][]*domain.RPCForward
	// rpcRes 跨仓续接打开的目标仓库视图（请求结束统一关闭——覆盖全部子树
	// 使用期，避免"边界处关闭、递归时 DB 已关"的旧坑）
	rpcRes *seqRPCResources
	// todo22 §4.1：RPC 续接防环键（仓库|方法入口），跨请求子树共享
	rpcVisited map[string]bool
	// todo22 §3.3：当前包的 DI 注册表（key → 注册点；扫描一次缓存）
	diRegs map[string][]seqDIReg
	// todo22 §2.1：当前分支条件（if/else 进入分支时设置，赋值/字面量绑定
	// 随值保存——"在什么条件下成立"是证据的一部分）
	branchCond string
	pkgSet     map[string]bool // todo13-13：已索引包集合（调用分类用，一次构建）
	// todo13-14：证据/输出选项与身份（argv 为权威；版本来自实际构建）
	Evidence   bool
	Format     string
	Out        string
	buildID    string
	builtAt    string
	cliVersion string
	// todo13-17：一次分析的源码解析缓存（同文件多函数展开不重复读盘/解析）
	fileCache map[string]*seqParsedFile
	// todo22 §2：函数值/闭包数据流——当前函数身份/文件上下文/值环境，
	// 以及本次调用带给被展开函数的实参值（按位置）与字面量防环集合。
	curSym      string
	curTgts     *seqTargets
	curCtx      *seqFileCtx
	curRecvType string
	curRecvDecl map[string]string
	env         *seqEnv
	paramValues []seqValue
	litVisited  map[string]bool
	tgtsCache   map[string]*seqTargets
	// 内部（todo10-5）：场景统计（meta 汇总；req 按值传递——用指针共享）
	sceneCount *sceneSummary
}

// seqIfaceCached 一次请求内缓存下来的接口实现解析结果（chosen/candidates
// 同源：首次查询用 InterfaceMethodImpl 的选定项，缓存命中复用同一结果）。
type seqIfaceCached struct {
	chosen string
	cands  []string
	ok     bool
}

// seqFocus 一次查询的焦点配置（--focus 解析结果，跨递归共享）。
type seqFocus struct {
	validated bool            // todo12-9：目标存在性/歧义已校验
	returns   bool            // 关注返回值
	fields    []string        // 字段路径（后缀匹配）
	fieldIDs  []string        // todo12-9：解析后的类型限定字段身份（pkg.T.F）
	writers   map[string]bool // 写这些字段的函数（canonical ID——被调者命中即关注）
	matched   int             // 命中的节点数（0 = 未命中，meta 标记）
}
