package action

import (
	"context"
	"errors"
	"go/ast"
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
	// indexSHA 当前索引 commit（todo12-14：证据可复现性——内部填充）
	indexSHA string
	// entryID 入口 canonical ID（证据命令重放整棵分析树，而不是只查被调节点）
	entryID string
	// 内部：递归展开路径防环（R97——grpc 实现内部调接口、接口具体化
	// 回到同一 grpc 实现时自环；路径语义：进入标记、退出清除，不同
	// 分支互不影响）
	visited map[string]bool
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
	// 内部（todo11-2）：当前函数 canonical ID / 文件（值流解析向上找调用点用）
	incomingArgs []string
	incomingSrc  []string // todo12-3：入站实参来源
	curBody      *ast.BlockStmt
	curParams    *ast.FieldList
	curFile      string
	sceneArgs    map[string]string
	sceneSrc     map[string]string // todo12-3：形参 → 值来源（分支判定证据）
	sceneWhy     map[string]string // todo13-3：形参 → 未知原因（partial 诊断）
	pkgSet       map[string]bool   // todo13-13：已索引包集合（调用分类用，一次构建）
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
	// 内部（todo10-5）：场景统计（meta 汇总；req 按值传递——用指针共享）
	sceneCount *sceneSummary
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
