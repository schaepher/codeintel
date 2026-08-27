package cli

// R77 wiki 特性转命令的公共 helper：query architecture/er/processes/
// module 复用 wiki 渲染层的数据函数（wiki 与命令行同源——wiki 渲染
// 用的数据命令行可查）。

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"gopkg.in/yaml.v3"
)

// loadWikiCfg 读 wiki.yaml（yamlPath 优先，否则仓库根 wiki.yaml；不存
// 在返回空配置——查询命令不强制 domains，有则用，无则纯代码事实）。
func loadWikiCfg(abs, yamlPath string) wikiConfig {
	cfg := wikiConfig{}
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		if yamlPath != "" {
			return cfg
		}
		if b, err = os.ReadFile(filepath.Join(abs, "wiki.yaml")); err != nil {
			return cfg
		}
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return wikiConfig{}
	}
	return cfg
}

// hideTableFrom 从配置构建隐藏表集合（tables.hidden）。
func hideTableFrom(cfg wikiConfig) map[string]bool {
	out := map[string]bool{}
	for _, t := range cfg.Tables {
		if t.Hidden {
			out[t.Name] = true
		}
	}
	return out
}

// wikiDataFor 加载模块 wiki 数据（buildRepo 读 go.mod 列表 + WikiData
// 查询聚合——architecture/processes/module 命令与 MCP 共用）。
func wikiDataFor(acts *action.Actions, abs string) ([]*domain.WikiModule, error) {
	repo, err := buildRepo(abs)
	if err != nil {
		return nil, err
	}
	return acts.WikiData(repo.Modules)
}

// dispatchWikiSub R77 wiki 特性子命令分发（query_dispatch.go 行数治理
// 拆出——packages/architecture/er/processes/module）。done=false 表示
// 非本组子命令（继续走通用 switch）。
func dispatchWikiSub(sub string, acts *action.Actions, db *sqlite.DB, abs string, f queryFlags, opts outputOpts) (int, bool) {
	if sub == "packages" {
		return cmdQueryPackages(acts, opts), true
	}
	if sub == "architecture" || sub == "er" || sub == "processes" {
		cfg := loadWikiCfg(abs, f.yamlPath)
		repo, err := buildRepo(abs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1, true
		}
		data, err := acts.WikiData(repo.Modules)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1, true
		}
		switch sub {
		case "architecture":
			return cmdQueryArchitecture(acts, sqlite.NewRepo(db), data, cfg, opts, f.format), true
		case "er":
			return cmdQueryER(acts, cfg, opts, f.format), true
		case "processes":
			return cmdQueryProcesses(acts, sqlite.NewRepo(db), abs, data, cfg, opts, f), true
		}
	}
	if sub == "module" {
		return cmdQueryModule(acts, abs, f, opts), true
	}
	return 0, false
}

// wikiCtx 查询命令共用的渲染上下文构造（processes/architecture 复用
// wiki 渲染函数——grpcServiceList/httpProcEntries/archLayeredMermaid
// 依赖 rc 字段）。
func wikiCtx(acts *action.Actions, data []*domain.WikiModule, cfg wikiConfig, abs string, maxEntries int) *wikiRenderCtx {
	return &wikiRenderCtx{
		acts:       acts,
		data:       data,
		cfg:        cfg,
		Diagram:    "mermaid",
		MaxEntries: maxEntries,
		RepoAbs:    abs,
	}
}
