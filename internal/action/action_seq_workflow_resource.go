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

// seqResEvent 一次资源改写事件。
//
// die > 0 表示该事件所在分支在 die（块结束偏移）之后不可达（例如
// `if stop { DB=nil; return }`）：它只能影响 die 之前的读取，不能污染
// 继续执行路径上的资源绑定。
type seqResEvent struct {
	off int
	die int
}

// seqEntryFactData 单个入口的资源改写事件与提交证明。
type seqEntryFactData struct {
	events        map[string][]seqResEvent // resource symbol → 事件（按偏移升序）
	commitSites   map[string]bool          // 已证明成功检查的 Commit 调用位置（file:offset）
	partial       bool
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
		if ev.off >= offset {
			continue
		}
		if ev.die > 0 && offset >= ev.die {
			// 该分支在到达此读取之前已经退出（return/panic）——改写不可达。
			continue
		}
		return true
	}
	return false
}

// commitProven 该写入所在事务是否在入口成功路径上被检查过 Commit。
//
// B06e：身份 = 事务来源（Begin 调用评估）+ 该来源的 **Commit 调用位置**：
//   - 同一声明重绑到另一事务后，Commit 属于另一个 origin，不能证明原写入；
//   - 同一 helper 被调用两次产生两个事务（origin 不同），不能互相借用；
//   - 同一事务的所有 Commit 调用点都必须通过成功检查（分支里遗漏一个即失败）。
func (f *seqWorkflowFacts) commitProven(entries []SeqWorkflowEntry, w seqWFWrite) bool {
	d := f.byEntry[w.Entry]
	if d == nil || !w.Tx || w.TxID == "" {
		return false
	}
	sites := seqWFCommitSites(entries, w.Entry, w.TxID)
	if len(sites) == 0 {
		return false
	}
	for _, site := range sites {
		if !d.commitSites[site] {
			return false
		}
	}
	return true
}

// seqWFCommitSites 入口树里提交该事务（origin id）的 Commit 调用位置键。
func seqWFCommitSites(entries []SeqWorkflowEntry, entry, txID string) []string {
	tree := seqWFEntryTree(entries, entry)
	if tree == nil {
		return nil
	}
	var out []string
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			tx := n.Evidence
			if tx != nil && tx.Transaction != nil && tx.Transaction.Action == SeqTxCommit &&
				tx.Transaction.Origin != nil && tx.Transaction.Origin.ID == txID &&
				n.call != nil && n.call.Lparen.IsValid() {
				// 位置键用 Commit 调用在**其所在文件**里的偏移（与 AST 事实同口径）。
				origin := tx.Transaction.Origin
				offset := 0
				if n.callOffset > 0 {
					offset = n.callOffset
				}
				rel := tx.CallFile
				if rel == "" && origin != nil {
					rel = origin.File
				}
				if offset > 0 && rel != "" {
					out = append(out, filepathBase(rel)+":"+itoaSeq(offset))
				}
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(tree.Nodes)
	sort.Strings(out)
	return out
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
	out := &seqEntryFactData{events: map[string][]seqResEvent{}, commitSites: map[string]bool{}}
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
	entryFile := files[filepath.Base(n.FilePath)]
	if entryFile == nil {
		return out
	}
	fn := findFuncByLine(entryFile.file, entryFile.fset, n.LineStart)
	if fn == nil || fn.Body == nil {
		return out
	}
	pkgVars := seqPkgVarNames(files)
	pkgVarObjs := seqPkgVarObjects(files)
	b := &seqResourceFactBuilder{out: out, pkg: pkg, files: files, pkgVars: pkgVars,
		pkgVarObjs: pkgVarObjs, repo: repo, entry: entry}
	// 入口函数里的 Commit 检查（路径敏感；只认入口自身，嵌套函数 return 不算）。
	out.commitSites = seqCommitSuccessVars(fn, entryFile, files)
	b.walkFunc(fn, entryFile, 0, true, 0)
	for res := range out.events {
		sort.SliceStable(out.events[res], func(i, j int) bool {
			return out.events[res][i].off < out.events[res][j].off
		})
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
