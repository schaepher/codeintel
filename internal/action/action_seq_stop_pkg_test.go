package action

// 停止包（seq.stop_packages）配置对代码级时序的作用：命中即不深入展开，
// 节点仍保留（R95）。从 action_seq_code_test.go 拆出——行数治理。

import "testing"

// TestCodeSequenceStopPkg：R83——停止包命中 → depth 2 不展开内部
// （节点保留 Nodes 空；StopPackages 由 cli 传入）。
func TestCodeSequenceStopPkg(t *testing.T) {
	acts, dir := seedNestedSeqRepo(t)
	// 无停止包：depth 2 展开 helper
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Prepare", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Nodes) != 1 || len(root.Nodes[0].Nodes) != 1 {
		t.Fatalf("无停止包 depth 2 应展开: %+v", root.Nodes)
	}
	// svc 在停止列表：depth 2 不展开（节点保留 Nodes 空）
	root2, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "Prepare", RepoAbs: dir, Depth: 2, StopPackages: []string{"example.com/m/svc"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(root2.Nodes) != 1 || len(root2.Nodes[0].Nodes) != 0 {
		t.Fatalf("停止包应不展开内部: %+v", root2.Nodes)
	}
	if root2.Nodes[0].Label != "svc.LoadItems" {
		t.Errorf("节点应保留（不深入）: %+v", root2.Nodes[0])
	}
}
