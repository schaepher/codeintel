package action

// R72/R71/R57 domains AI 编排测试（批次 C 随迁自 cli/wiki_r72_test.go、
// wiki_ai_toggle_test.go、wiki_r71_test.go）：AI 输出 JSON 文件兜底、
// 超时后检查文件、prompt 约束/渲染基准/写文件要求。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// timeoutErr 模拟 agent 超时错误。
type timeoutErr struct{}

func (e *timeoutErr) Error() string { return "agent claude 超时（超过 6m0s）" }

// openAITestRepo 建仓库并补包节点（ParseDomains 校验需要归属存在——
// KindPackage 节点 ID 为路径形态 symbol:go:<path>，SymbolPkg 提取）。
func openAITestRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m", Kind: domain.KindPackage, Name: "example.com/m"},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestAnalyzeDomainsJSONFile：AI 写 JSON 文件 → 程序读文件解析（不依赖
// 响应文本——响应解析失败/超时不影响，文件是权威来源）。
func TestAnalyzeDomainsJSONFile(t *testing.T) {
	a, dir := openAITestRepo(t)
	var outFile string
	res, err := a.AnalyzeDomains(DomainsRequest{
		RepoAbs: dir, Agent: "claude", YAMLPath: filepath.Join(dir, "wiki.yaml"),
		AgentRunner: func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
			// AI 写 JSON 文件（domains-ai.json）
			outFile = filepath.Join(dir, ".codeintel", "domains-ai.json")
			b := []byte(`{"domains": [{"name": "交易域", "description": "交易", "packages": ["example.com/m"]}]}`)
			if err := os.WriteFile(outFile, b, 0o644); err != nil {
				return "", err
			}
			return "done", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Doms) != 1 || res.Doms[0].Name != "交易域" {
		t.Fatalf("doms = %+v; want 交易域（读 JSON 文件）", res.Doms)
	}
	if len(res.Warns) > 0 {
		t.Errorf("warns = %v; want 无", res.Warns)
	}
	if outFile == "" {
		t.Error("AI 应写 JSON 输出文件")
	}
}

// TestAnalyzeDomainsTimeoutFileCheck：AI 超时但文件已写（AI 实际完成）
// → 用文件结果，不重试（R72：超时后检查输出文件）。
func TestAnalyzeDomainsTimeoutFileCheck(t *testing.T) {
	a, dir := openAITestRepo(t)
	res, err := a.AnalyzeDomains(DomainsRequest{
		RepoAbs: dir, Agent: "claude", YAMLPath: filepath.Join(dir, "wiki.yaml"),
		AgentRunner: func(agent, prompt string, timeout time.Duration, dir string) (string, error) {
			// 超时失败，但 AI 已把结果写入文件
			b := []byte(`{"domains": [{"name": "商品域", "description": "商品", "packages": ["example.com/m"]}]}`)
			_ = os.WriteFile(filepath.Join(dir, ".codeintel", "domains-ai.json"), b, 0o644)
			return "", &timeoutErr{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Doms) != 1 || res.Doms[0].Name != "商品域" {
		t.Fatalf("doms = %+v; want 商品域（超时后读已写文件——不重试）", res.Doms)
	}
}

// TestDomainPromptOutputFile：prompt 要求 AI 写 JSON 文件（响应只回
// done——文本解析不再可靠）。
func TestDomainPromptOutputFile(t *testing.T) {
	p := DomainPrompt("facts.json", "")
	for _, want := range []string{"domains-ai.json", "JSON", "Write"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt 应要求写 JSON 文件 %q", want)
		}
	}
}

// TestDomainPromptExtra：DomainPrompt 带 extraPrompt 时含用户约束段。
func TestDomainPromptExtra(t *testing.T) {
	p := DomainPrompt("facts.json", "")
	if strings.Contains(p, "用户额外约束") {
		t.Error("无 extraPrompt 时不应含约束段")
	}
	p2 := DomainPrompt("facts.json", "商品域：交易域")
	for _, want := range []string{"facts.json", "用户额外约束", "商品域：交易域"} {
		if !strings.Contains(p2, want) {
			t.Errorf("prompt 应含 %q", want)
		}
	}
}

// TestDomainPromptRenderLimit：R71——prompt 告知渲染基准（域内实体数/
// 调用边 500 上限——AI 输出 domains 时自带子域划分与过大判断）。
func TestDomainPromptRenderLimit(t *testing.T) {
	p := DomainPrompt("facts.json", "")
	for _, want := range []string{"渲染", "500", "实体数"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt 应含渲染基准 %q", want)
		}
	}
}
