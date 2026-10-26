package action

// todo13-3：取址/别名传入调用后，旧字面量定义失效。
//   - rewrite(&region) / p := &region; rewrite(p) → Route(region) 的 region=unknown
//   - 两个分支都保留（不得按旧 CN 裁掉 Notify）
//   - 完整性 partial，原因/位置指向实际改写调用
//   - 两层传参来源是真实文件正行号（无 def@L0）
//   - 分析深度上限不得静默保留确定值

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const sceneRewriteSrc = `package m

func Audit() {}

func Notify() {}

func Route(region string) {
	if region == "CN" {
		Audit()
	} else {
		Notify()
	}
}

func Rewritten(rewrite func(*string)) {
	region := "CN"
	rewrite(&region)
	Route(region)
}

func Aliased(rewrite func(*string)) {
	region := "CN"
	p := &region
	rewrite(p)
	Route(region)
}

func TwoLayer() {
	region := "CN"
	Mid(region)
}

func Mid(region string) {
	Route(region)
}

func Deep() {
	region := "CN"
	if true {
		if true {
			if true {
				if true {
					if true {
						if true {
							if true {
								if true {
									if true {
										if true {
											if true {
												if true {
													if true {
														if true {
															Route(region)
														}
													}
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}
`

func seedSceneRewriteRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sceneRewriteSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	mk := func(name string, line int) *domain.CodeEntity {
		return &domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/m:" + name),
			Kind: domain.KindFunction, Name: name, FilePath: "main.go", LineStart: line}
	}
	nodes := []*domain.CodeEntity{
		mk("Route", 7), mk("Rewritten", 15), mk("Aliased", 21), mk("TwoLayer", 28),
		mk("Mid", 33), mk("Deep", 37), mk("Audit", 3), mk("Notify", 5),
	}
	call := func(caller string, line int) *domain.Fact {
		return &domain.Fact{SourceID: domain.CanonicalID("symbol:go:example.com/m:" + caller),
			TargetID: "symbol:go:example.com/m:Route", Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": float64(line)}}
	}
	mid := &domain.Fact{SourceID: "symbol:go:example.com/m:Mid", TargetID: "symbol:go:example.com/m:Route",
		Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(34)}}
	top := &domain.Fact{SourceID: "symbol:go:example.com/m:TwoLayer", TargetID: "symbol:go:example.com/m:Mid",
		Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(30)}}
	facts := []*domain.Fact{mid, top, call("Rewritten", 18), call("Aliased", 25), call("Deep", 53)}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "rewrite1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func rewriteRoot(t *testing.T, acts *Actions, dir, entry string) *CodeSeqNode {
	t.Helper()
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:" + entry, RepoAbs: dir, Depth: 3})
	if err != nil || root == nil {
		t.Fatalf("%s CodeSequence: %v root=%v", entry, err, root)
	}
	return root
}

func rewriteFit(t *testing.T, root *CodeSeqNode) SceneFit {
	t.Helper()
	fit, ok := sceneBranches(root)[`region == "CN"`]
	if !ok {
		t.Fatalf("内层分支应带场景判定: %+v", sceneBranches(root))
	}
	return fit
}

// R3/A3：取址传入未知调用 → 旧定义失效，两个分支保留，partial 指向调用位置。
func TestSceneRewriteByAddressedCall(t *testing.T) {
	acts, dir := seedSceneRewriteRepo(t)
	root := rewriteRoot(t, acts, dir, "Rewritten")
	fit := rewriteFit(t, root)
	if fit.Status != SceneUnknown {
		t.Fatalf("rewrite(&region) 后应为 unknown（不能沿用 CN），实为 %+v", fit)
	}
	if fit.Expected == "CN" {
		t.Errorf("不得把旧 CN 当作 Route 的已知实参: %+v", fit)
	}
	if fit.Cause == "" || !strings.Contains(fit.Cause, "call-may-write") {
		t.Errorf("原因应指向改写调用: %+v", fit)
	}
	if !strings.Contains(fit.Source, "@L") || strings.Contains(fit.Source, "@L0") {
		t.Errorf("来源应为真实文件正行号: %+v", fit)
	}
	// 两分支都在（不能只保留 Audit）
	labels := map[string]bool{}
	walkSeqNode(root, func(n *CodeSeqNode) {
		if n.Kind == "call" {
			labels[n.Label] = true
		}
	})
	if !labels["Audit"] || !labels["Notify"] {
		t.Errorf("unknown 时两分支都应保留: %+v", labels)
	}
	// 完整性 partial + 原因
	if root.Meta == nil || root.Meta.Scene == nil || !root.Meta.Scene.Partial {
		t.Errorf("应标记场景完整性 partial: %+v", root.Meta)
	}
	if root.Meta != nil && root.Meta.Scene != nil && len(root.Meta.Scene.Causes) == 0 {
		t.Errorf("partial 应给出原因: %+v", root.Meta.Scene)
	}
}

// R3/A3：别名传入未知调用同样失效。
func TestSceneRewriteByAliasedCall(t *testing.T) {
	acts, dir := seedSceneRewriteRepo(t)
	fit := rewriteFit(t, rewriteRoot(t, acts, dir, "Aliased"))
	if fit.Status != SceneUnknown {
		t.Fatalf("别名 p:=&region; rewrite(p) 后应 unknown，实为 %+v", fit)
	}
}

// A3：两层传参来源是真实定义位置（无 def@L0）。
func TestSceneRewriteTwoLayerSourceLine(t *testing.T) {
	acts, dir := seedSceneRewriteRepo(t)
	fit := rewriteFit(t, rewriteRoot(t, acts, dir, "TwoLayer"))
	if fit.Status != SceneTaken {
		t.Fatalf("两层传参字面量 CN 应 taken，实为 %+v", fit)
	}
	if !strings.Contains(fit.Source, "@L") || strings.Contains(fit.Source, "@L0") {
		t.Errorf("来源须为真实定义行（不得 def@L0）: %+v", fit)
	}
}

// R3/A3：分析深度上限不得静默保留确定值。
func TestSceneRewriteDepthLimitUnknown(t *testing.T) {
	acts, dir := seedSceneRewriteRepo(t)
	fit := rewriteFit(t, rewriteRoot(t, acts, dir, "Deep"))
	if fit.Status == SceneTaken {
		t.Fatalf("超出分析深度上限不得保留确定值: %+v", fit)
	}
	if fit.Reason == "" {
		t.Errorf("应说明 unknown 原因: %+v", fit)
	}
}

// walkSeqNode 遍历整棵时序节点树。
func walkSeqNode(n *CodeSeqNode, fn func(*CodeSeqNode)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Nodes {
		walkSeqNode(c, fn)
	}
	for _, c := range n.Else {
		walkSeqNode(c, fn)
	}
}
