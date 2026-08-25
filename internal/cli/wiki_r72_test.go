package cli

// R72 测试：AI 输出 JSON 文件（程序读文件转 YAML——不再依赖响应文本
// 解析）；超时后检查输出文件（AI 可能已写完——不盲目完整重试）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestAnalyzeDomainsJSONFile：AI 写 JSON 文件 → 程序读文件解析（不依赖
// 响应文本——响应解析失败/超时不影响，文件是权威来源）。
func TestAnalyzeDomainsJSONFile(t *testing.T) {
	dir := seedRepo(t)
	// 包节点（parseDomains 校验需要归属存在）
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	// 包节点（parseDomains 校验需要归属存在——KindPackage 节点 ID 为
	// 路径形态 symbol:go:<path>，symbolPkg 提取完整路径）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m", Kind: domain.KindPackage, Name: "example.com/m"},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	acts := action.New(r)
	var outFile string
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		// AI 写 JSON 文件（domains-ai.json）
		outFile = filepath.Join(dir, ".codeintel", "domains-ai.json")
		b := []byte(`{"domains": [{"name": "交易域", "description": "交易", "packages": ["example.com/m"]}]}`)
		if err := os.WriteFile(outFile, b, 0o644); err != nil {
			return "", err
		}
		return "done", nil
	})
	defer restore()
	cfg := wikiConfig{}
	doms, warns := analyzeDomains(dir, &cfg, acts, r, "claude", filepath.Join(dir, "wiki.yaml"), "", "")
	if len(doms) != 1 || doms[0].Name != "交易域" {
		t.Fatalf("doms = %+v; want 交易域（读 JSON 文件）", doms)
	}
	if len(warns) > 0 {
		t.Errorf("warns = %v; want 无", warns)
	}
	if outFile == "" {
		t.Error("AI 应写 JSON 输出文件")
	}
}

// TestAnalyzeDomainsTimeoutFileCheck：AI 超时但文件已写（AI 实际完成）
// → 用文件结果，不重试（R72：超时后检查输出文件）。
func TestAnalyzeDomainsTimeoutFileCheck(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m", Kind: domain.KindPackage, Name: "example.com/m"},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	acts := action.New(r)
	restore := injectRunner(t, func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
		// 超时失败，但 AI 已把结果写入文件
		b := []byte(`{"domains": [{"name": "商品域", "description": "商品", "packages": ["example.com/m"]}]}`)
		_ = os.WriteFile(filepath.Join(dir, ".codeintel", "domains-ai.json"), b, 0o644)
		return "", &timeoutErr{}
	})
	defer restore()
	cfg := wikiConfig{}
	doms, _ := analyzeDomains(dir, &cfg, acts, r, "claude", filepath.Join(dir, "wiki.yaml"), "", "")
	if len(doms) != 1 || doms[0].Name != "商品域" {
		t.Fatalf("doms = %+v; want 商品域（超时后读已写文件——不重试）", doms)
	}
}

// timeoutErr 模拟 agent 超时错误。
type timeoutErr struct{}

func (e *timeoutErr) Error() string { return "agent claude 超时（超过 6m0s）" }

// TestDomainPromptOutputFile：prompt 要求 AI 写 JSON 文件（响应只回
// done——文本解析不再可靠）。
func TestDomainPromptOutputFile(t *testing.T) {
	p := domainPrompt("facts.json", "")
	for _, want := range []string{"domains-ai.json", "JSON", "Write"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt 应要求写 JSON 文件 %q", want)
		}
	}
}
