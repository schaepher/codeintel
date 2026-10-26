package action

// todo13-14：证据身份（argv/版本/构建/配置）与比对。
//   - 结构化 argv 含全部 scene/focus/evidence/format/out/过滤/stop_packages
//   - 同 SHA 重建 → 构建身份差异（源码 SHA 不变）
//   - 过滤/停止包变化 → 配置身份差异；同身份 → 无差异

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func setBuildMeta(t *testing.T, dir, buildID, sha string) {
	t.Helper()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.NewRepo(db).Save(&domain.BuildMeta{
		BuildID: buildID, ToolName: "test", Status: domain.BuildSuccess, CommitSHA: sha}); err != nil {
		t.Fatal(err)
	}
}

func TestSeqEvidenceIdentityAndCompare(t *testing.T) {
	acts, dir := seedSceneReachRepo(t)
	setBuildMeta(t, dir, "build-1", "sha-same")
	base := CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Route", RepoAbs: dir, Depth: 2,
		StopPackages: []string{"example.com/skip"},
		Filter:       SeqFilter{Pkgs: []string{"example.com/f"}, Fns: []string{"Fn"}},
		Focus:        []string{"return"}, Scene: NewScene([]string{"region=#1"}),
		Evidence: true, Format: "mermaid", Out: "out file.txt",
	}
	base.SetCLIVersion("cli-1")
	root, err := acts.CodeSequence(base)
	if err != nil || root == nil || root.Meta == nil || root.Meta.Evidence == nil {
		t.Fatalf("应输出 meta.evidence: %v %+v", err, root)
	}
	id := root.Meta.Evidence
	if id.IndexSHA != "sha-same" || id.BuildID != "build-1" || id.CLI != "cli-1" ||
		id.Analyzer == "" || id.ConfigHash == "" {
		t.Errorf("身份字段缺失: %+v", id)
	}
	argv := strings.Join(id.Argv, " ")
	for _, want := range []string{"--scene 'region=#1'", "--focus 'return'", "--evidence",
		"--format 'mermaid'", "--out 'out file.txt'", "--depth 2"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv 应含 %q: %s", want, argv)
		}
	}
	if id.Filter == nil || len(id.Filter.Pkgs) == 0 || len(id.StopPkgs) != 1 {
		t.Errorf("请求快照应含 filter/stop_packages: %+v", id)
	}

	// 同身份：无差异
	prior, _ := json.Marshal(root)
	diffs, err := CompareSeqEvidenceIdentity(id, prior)
	if err != nil || len(diffs) != 0 {
		t.Fatalf("同身份不应有差异: diffs=%v err=%v", diffs, err)
	}

	// 同 SHA 重建：构建身份变化
	setBuildMeta(t, dir, "build-2", "sha-same")
	root2, err := acts.CodeSequence(base)
	if err != nil || root2 == nil {
		t.Fatal(err)
	}
	diffs, _ = CompareSeqEvidenceIdentity(root2.Meta.Evidence, prior)
	if len(diffs) == 0 {
		t.Fatal("同 SHA 重建应报告构建身份变化")
	}
	joined := strings.Join(diffs, " | ")
	if !strings.Contains(joined, "索引构建身份变化") {
		t.Errorf("应报告构建身份变化: %s", joined)
	}
	if strings.Contains(joined, "源码 SHA 变化") {
		t.Errorf("源码 SHA 未变不应报告变化: %s", joined)
	}

	// 配置变化：过滤/停止包差异
	prior2, _ := json.Marshal(root2)
	changed := base
	changed.StopPackages = []string{"example.com/other"}
	changed.Filter = SeqFilter{Pkgs: []string{"example.com/g"}}
	root3, err := acts.CodeSequence(changed)
	if err != nil || root3 == nil {
		t.Fatal(err)
	}
	diffs, _ = CompareSeqEvidenceIdentity(root3.Meta.Evidence, prior2)
	joined = strings.Join(diffs, " | ")
	if !strings.Contains(joined, "配置身份变化") ||
		!strings.Contains(joined, "example.com/other") ||
		!strings.Contains(joined, "example.com/g") {
		t.Errorf("应报告配置差异: %s", joined)
	}

	// 旧格式/坏输入
	if _, err := CompareSeqEvidenceIdentity(id, []byte("not json")); err == nil {
		t.Error("坏 JSON 应报错")
	}
}
