//go:build integration

package integration

// todo22 §9.1：八个同步流程（SourceA/B × 新建实体/新增子项/编辑子项/编辑实体），
// 逐项独立验收：入口 → 检查清单（4/7）→ trace 提交 → 准确 RPC 服务端续接 →
// 非空装饰器 + 事务回调 → 存储终点（按操作不同）→ trace 批准；人工 FinishReview
// 次数为零（这些流程没有人工审核）。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// flowMatrixCLI 构建二进制并在 flowmatrix 夹具上 init（返回 bin 与仓库目录）。
// 用外部进程读输出（大树的 JSON 写入管道缓冲会阻塞）。
func flowMatrixCLI(t *testing.T) (string, string) {
	t.Helper()
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(wd)
	bin := filepath.Join(t.TempDir(), "codeintel")
	build := exec.Command("go", "build", "-o", bin, "./cmd/codeintel")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build codeintel: %v\n%s", err, out)
	}
	f := writeFlowMatrix(t, nil)
	if code, out := flowRun(t, bin, "init", "--repo", f.dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d\n%s", code, out)
	}
	return bin, f.dir
}

// seqEv 一个节点的证据摘要（按先序遍历顺序收集）。
type seqEv struct {
	label  string
	sym    string
	source string
	reason string
}

// collectSeqEv 先序收集节点证据（含分支/else）。
func collectSeqEv(n map[string]any, out *[]seqEv) {
	if n == nil {
		return
	}
	ev, _ := n["evidence"].(map[string]any)
	e := seqEv{label: str(n["Label"])}
	if ev != nil {
		e.sym, _ = ev["symbol_id"].(string)
		e.source, _ = ev["source"].(string)
		e.reason, _ = ev["reason"].(string)
	}
	*out = append(*out, e)
	for _, key := range []string{"Nodes", "Else"} {
		cs, _ := n[key].([]any)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok {
				collectSeqEv(m, out)
			}
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// countSym 指定 canonical ID 的节点数。
func countSym(evs []seqEv, sym string) int {
	n := 0
	for _, e := range evs {
		if e.sym == sym {
			n++
		}
	}
	return n
}

// firstIndex 首个符号/标签命中位置（-1 = 未出现）。
func firstIndex(evs []seqEv, pred func(seqEv) bool) int {
	for i, e := range evs {
		if pred(e) {
			return i
		}
	}
	return -1
}

// seqFlows §9.1 的八个入口（编号 21–28）与各自存储终点。
var seqFlows = []struct {
	num      int
	name     string
	tx       string // 事务/装饰器方法（每个操作独立）
	terminal string // 存储终点方法
}{
	{21, "Flow21NewEntityA", "(App).txInsert", "(Store).Insert"},
	{22, "Flow22NewChildA", "(App).txInsertChild", "(Store).InsertChild"},
	{23, "Flow23EditChildA", "(App).txUpdateChild", "(Store).UpdateChild"},
	{24, "Flow24NewEntityB", "(App).txInsert", "(Store).Insert"},
	{25, "Flow25NewChildB", "(App).txInsertChild", "(Store).InsertChild"},
	{26, "Flow26EditChildB", "(App).txUpdateChild", "(Store).UpdateChild"},
	{27, "Flow27EditEntityA", "(App).txUpdateEntity", "(Store).UpdateEntity"},
	{28, "Flow28EditEntityB", "(App).txUpdateEntity", "(Store).UpdateEntity"},
}

func TestTodo22SyncFlows(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	bin, dir := flowMatrixCLI(t)
	const pkg = "symbol:go:example.com/flowmatrix:"
	for _, flow := range seqFlows {
		for _, sc := range []struct {
			scene  string
			checks int
		}{{"Source=7", 4}, {"Source=19", 7}} {
			t.Run(fmt.Sprintf("%02d-%s", flow.num, sc.scene), func(t *testing.T) {
				code, out := flowRun(t, bin, "query", "sequence", pkg+"(App)."+flow.name,
					"--repo", dir, "--code", "--depth", "24", "--scene", sc.scene, "--json")
				if code != 0 {
					t.Fatalf("sequence exit=%d\n%s", code, out)
				}
				var root map[string]any
				if err := json.Unmarshal([]byte(out), &root); err != nil {
					t.Fatalf("JSON: %v\n%s", err, out)
				}
				var evs []seqEv
				collectSeqEv(root, &evs)

				// ① 检查清单按来源裁剪（4/7），每项恰好一次
				for i := 1; i <= 7; i++ {
					want := 0
					if i <= sc.checks {
						want = 1
					}
					if got := countSym(evs, fmt.Sprintf("%sCheck%d", pkg, i)); got != want {
						t.Errorf("Check%d 执行 %d 次，期望 %d", i, got, want)
					}
				}
				// ② trace 提交 → RPC → 存储 → trace 批准（顺序可核对）
				save := firstIndex(evs, func(e seqEv) bool { return e.sym == pkg+"SaveTrace" })
				remote := firstIndex(evs, func(e seqEv) bool { return e.sym == pkg+"ApplyRemote" })
				store := firstIndex(evs, func(e seqEv) bool { return strings.HasSuffix(e.sym, flow.terminal) })
				approve := firstIndex(evs, func(e seqEv) bool { return e.sym == pkg+"ApproveTrace" })
				if save < 0 || remote < 0 || store < 0 || approve < 0 {
					t.Fatalf("链路缺环节：save=%d remote=%d store(%s)=%d approve=%d", save, remote,
						flow.terminal, store, approve)
				}
				if !(save < remote && remote < store && store < approve) {
					t.Errorf("顺序应为 SaveTrace < ApplyRemote < %s < ApproveTrace（实际 %d/%d/%d/%d）",
						flow.terminal, save, remote, store, approve)
				}
				// ③ RPC 服务端续接（准确注册身份 + 服务端实现）
				rpc := firstIndex(evs, func(e seqEv) bool { return e.source == "rpc" })
				if rpc < 0 {
					t.Fatalf("应有 RPC 服务端续接节点")
				}
				if evs[rpc].sym != pkg+"(AccountServer).Apply" {
					t.Errorf("RPC 目标 = %q", evs[rpc].sym)
				}
				serverApply := firstIndex(evs, func(e seqEv) bool { return e.sym == pkg+"(AccountServer).Apply" })
				if serverApply < 0 {
					t.Errorf("应展开服务端实现 (AccountServer).Apply")
				}
				// ④ 非空装饰器 + 事务回调（部分节点只有标签/证据在 label 上）
				for _, want := range []string{flow.tx, "Execute", "MakeFirst", "First", "Second",
					"tx.Run", "BeforeFirst", "BeforeSecond", "AfterFirst", "AfterSecond"} {
					if firstIndex(evs, func(e seqEv) bool {
						return strings.Contains(e.sym, want) || strings.Contains(e.label, want)
					}) < 0 {
						t.Errorf("链路缺少 %s", want)
					}
				}
				// ⑤ DI → AOP wrapper → 业务实现 → 存储 API
				di := firstIndex(evs, func(e seqEv) bool {
					return e.source == "di" && strings.Contains(e.sym, "storeAOP")
				})
				if di < 0 {
					t.Errorf("DI/AOP 未解析（应有 di 证据且目标为 storeAOP 包装器）")
				} else if !strings.Contains(evs[di].reason, "aop") {
					t.Errorf("AOP 证据 reason = %q", evs[di].reason)
				}
				if firstIndex(evs, func(e seqEv) bool { return strings.HasSuffix(e.sym, "(DB).ExecContext") }) < 0 {
					t.Errorf("应到达存储 API（(DB).ExecContext）")
				}
				// ⑥ 人工审核次数为零（不凭业务名补入）
				if got := countSym(evs, pkg+"FinishReview"); got != 0 {
					t.Errorf("同步流程人工 FinishReview = %d，期望 0", got)
				}
				for _, bad := range []string{pkg + "FinishDataReview", pkg + "FinishServiceReview", pkg + "AutoApprove"} {
					if got := countSym(evs, bad); got != 0 {
						t.Errorf("同步流程不应出现 %s（%d 次）", bad, got)
					}
				}
			})
		}
	}
}
