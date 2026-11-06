package action

// U04bS/B06c：资源失效按执行位置计算；提交证明绑定实际写入事务。
//
// 位置事实来自入口函数及其可证明调用链（同包/仓内跨包，深度 ≤8）：
//   - 直接 `DB = ...` 在语句位置生效；
//   - 本地/闭包调用在**调用点**生效（闭包未调用不计）；
//   - 写入/读取按其 SQL 调用点偏移比较，后续改写不倒推。
//
// Commit 证明只认入口函数里 `if err = tx.Commit(); err != nil { return }`
// 且同一事务变量；另一事务/覆盖错误/嵌套 return 不算。

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/schaepher/codeintel/internal/domain"
)

const seqResourceFactMaxDepth = 8

var seqModulePathCache sync.Map

// seqEntryFactData 单个入口的资源改写事件与提交证明。
type seqEntryFactData struct {
	events     map[string][]int // resource symbol → 事件偏移（升序）
	commitVars map[string]bool  // 已证明成功检查 Commit 的事务变量名
	partial    bool
}

// seqWorkflowFacts 入口事实集合。
type seqWorkflowFacts struct {
	byEntry map[string]*seqEntryFactData
}

// mutatedAt 调用点之前是否已发生该资源的改写（严格小于调用偏移）。
func (f *seqWorkflowFacts) mutatedAt(entry, resource string, offset int) bool {
	d := f.byEntry[entry]
	if d == nil || resource == "" {
		return false
	}
	for _, ev := range d.events[resource] {
		if ev < offset {
			return true
		}
	}
	return false
}

// commitFor 该写入所在事务是否在入口成功路径上被检查过 Commit。
func (f *seqWorkflowFacts) commitFor(w seqWFWrite) bool {
	if !w.Tx || w.TxVar == "" {
		return false
	}
	d := f.byEntry[w.Entry]
	return d != nil && d.commitVars[w.TxVar]
}

func (f *seqWorkflowFacts) partial() bool {
	for _, d := range f.byEntry {
		if d != nil && d.partial {
			return true
		}
	}
	return false
}

// seqEntryFacts 解析入口函数与可证明调用链（事件位置 = 调用点偏移）。
func seqEntryFacts(a *Actions, repo, entry string) *seqEntryFactData {
	out := &seqEntryFactData{events: map[string][]int{}, commitVars: map[string]bool{}}
	if a == nil || entry == "" {
		return out
	}
	n, err := a.repo.GetSymbol(domain.CanonicalID(entry))
	if err != nil || n.FilePath == "" {
		return out
	}
	pkg := pkgOfID(domain.CanonicalID(entry))
	dir := filepath.Dir(filepath.Join(repo, n.FilePath))
	files := seqParsePackageDir(dir)
	if len(files) == 0 {
		return out
	}
	entryFile := files[n.FilePath]
	if entryFile == nil {
		return out
	}
	fn := findFuncByLine(entryFile.file, entryFile.fset, n.LineStart)
	if fn == nil || fn.Body == nil {
		return out
	}
	pkgVars := seqPkgVarNames(files)
	b := &seqResourceFactBuilder{out: out, pkg: pkg, files: files, pkgVars: pkgVars,
		repo: repo, entry: entry}
	// 入口函数里的 Commit 检查（只认入口自身，嵌套函数 return 不算）。
	out.commitVars = seqCommitGuardedVars(fn)
	b.walkFunc(fn, entryFile, 0, true, 0)
	for res := range out.events {
		sort.Ints(out.events[res])
	}
	return out
}

// seqWFExecOffset 入口树 path **首段**节点的执行偏移（读取/写入实际发生的
// 顶层调用点；嵌套被调函数体在进入该调用时才执行）。
func seqWFExecOffset(entries []SeqWorkflowEntry, entry, path string) int {
	tree := seqWFEntryTree(entries, entry)
	if tree == nil || path == "" {
		return 0
	}
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segs) < 2 || segs[0] != "Nodes" {
		return 0
	}
	idx, err := strconv.Atoi(segs[1])
	if err != nil || idx < 0 || idx >= len(tree.Nodes) {
		return 0
	}
	return tree.Nodes[idx].callOffset
}

// seqWFNodeAtPath 按 /Nodes/i|/Else/i 路径取节点。
func seqWFNodeAtPath(root *CodeSeqNode, path string) *CodeSeqNode {
	if root == nil || path == "" {
		return nil
	}
	cur := root
	for _, seg := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		parts := strings.SplitN(seg, "/", 1)
		idx := -1
		kind := ""
		if strings.HasPrefix(seg, "Nodes/") {
			kind = "Nodes"
			idx, _ = strconv.Atoi(strings.TrimPrefix(seg, "Nodes/"))
		} else if strings.HasPrefix(seg, "Else/") {
			kind = "Else"
			idx, _ = strconv.Atoi(strings.TrimPrefix(seg, "Else/"))
		}
		_ = parts
		if kind == "" || idx < 0 {
			return nil
		}
		list := cur.Nodes
		if kind == "Else" {
			list = cur.Else
		}
		if idx >= len(list) {
			return nil
		}
		cur = list[idx]
	}
	return cur
}
