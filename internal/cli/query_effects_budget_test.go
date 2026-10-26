package cli

// todo13-7：effects 路径预算诊断在 CLI 文本/JSON 中可观察（同一口径）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedEffectsBudgetCLI(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	var b strings.Builder
	b.WriteString("package m\n\nfunc Long(flags [30]bool) (int, bool) {\n")
	for i := 0; i < 30; i++ {
		b.WriteString("\tif flags[")
		b.WriteString(itoaInt(i))
		b.WriteString("] { return ")
		b.WriteString(itoaInt(i))
		b.WriteString(", false }\n")
	}
	b.WriteString("\treturn 30, true\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "long.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Long", Kind: domain.KindFunction, Name: "Long",
			FilePath: "long.go", LineStart: 3},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "effbudget", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func itoaInt(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestEffectsPathBudgetCLI(t *testing.T) {
	dir := seedEffectsBudgetCLI(t)
	txt := captureStdout(func() {
		if code := cmdQuery([]string{"effects", "symbol:go:example.com/m:Long", "--repo", dir}); code != 0 {
			t.Errorf("effects exit = %d", code)
		}
	})
	for _, want := range []string{"分析：partial", "24/24", "停止位置=long.go:", "未展开前沿=", "unknown"} {
		if !strings.Contains(txt, want) {
			t.Errorf("文本应含 %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "丢弃 1 个未展开终态") {
		t.Errorf("文本不得把前沿数当遗漏终态数:\n%s", txt)
	}

	js := captureStdout(func() {
		if code := cmdQuery([]string{"effects", "symbol:go:example.com/m:Long", "--repo", dir,
			"--json"}); code != 0 {
			t.Errorf("effects --json exit = %d", code)
		}
	})
	var res action.EffectsResult
	if err := json.Unmarshal([]byte(js), &res); err != nil {
		t.Fatalf("JSON: %v\n%s", err, js)
	}
	if res.Analysis == nil || res.Analysis.PathBudget == nil {
		t.Fatalf("JSON 应含 path_budget: %+v", res.Analysis)
	}
	pb := res.Analysis.PathBudget
	if pb.Used != 24 || pb.Limit != 24 || pb.StopLine == 0 || pb.Frontier == 0 ||
		pb.ExtraTerminals != "unknown" || !strings.HasSuffix(pb.StopFile, "long.go") {
		t.Errorf("JSON 预算口径应与文本一致: %+v", pb)
	}

	txt = captureStdout(func() {
		if code := cmdQuery([]string{"effects", "symbol:go:example.com/m:Long", "--repo", dir,
			"--path-budget", "64"}); code != 0 {
			t.Errorf("effects high budget exit = %d", code)
		}
	})
	if !strings.Contains(txt, "分析：complete") {
		t.Errorf("高预算应 complete:\n%s", txt)
	}
	if !strings.Contains(txt, "30,true") && !strings.Contains(txt, "30, true") {
		t.Errorf("高预算应含末尾 returns=[30 true]:\n%s", txt)
	}
}
