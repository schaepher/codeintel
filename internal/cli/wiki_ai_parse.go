package cli

// wiki --ai 的 AI 返回解析与 cfg 同步（从 wiki_ai.go 拆出——行数治理）。

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// stripYAMLFence 剥离 AI 输出的 ```yaml 围栏（缺尾围栏也容忍）。
func stripYAMLFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	lines = lines[1:]
	if n := len(lines); n > 0 && strings.HasPrefix(strings.TrimSpace(lines[n-1]), "```") {
		lines = lines[:n-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// parseAIDescription 解析 AI 返回 → 描述字符串。
func parseAIDescription(s string) (string, error) {
	s = stripYAMLFence(s)
	var m map[string]string
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		return "", fmt.Errorf("AI 返回不可解析: %v", err)
	}
	if m["description"] == "" {
		return "", fmt.Errorf("AI 返回缺 description 键")
	}
	return strings.TrimSpace(m["description"]), nil
}

// parseAIAlias 解析 AI 返回 → 表别名。
func parseAIAlias(s string) (string, error) {
	s = stripYAMLFence(s)
	var m map[string]string
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		return "", fmt.Errorf("AI 返回不可解析: %v", err)
	}
	if m["alias"] == "" {
		return "", fmt.Errorf("AI 返回缺 alias 键")
	}
	return strings.TrimSpace(m["alias"]), nil
}

// parseAIComments 解析 AI 返回 → 列 → 说明。
func parseAIComments(s string) (map[string]string, error) {
	s = stripYAMLFence(s)
	var items []struct {
		Name    string `yaml:"name"`
		Comment string `yaml:"comment"`
	}
	if err := yaml.Unmarshal([]byte(s), &items); err != nil {
		return nil, fmt.Errorf("AI 返回不可解析: %v", err)
	}
	out := map[string]string{}
	for _, it := range items {
		if it.Name != "" && it.Comment != "" {
			out[it.Name] = it.Comment
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("AI 返回缺 name/comment 条目")
	}
	return out, nil
}

// cfgSetModuleDesc 同步更新 cfg.Modules（渲染用）。
func cfgSetModuleDesc(cfg *wikiConfig, name, desc string) {
	for i := range cfg.Modules {
		if cfg.Modules[i].Name == name {
			cfg.Modules[i].Description = desc
			return
		}
	}
	cfg.Modules = append(cfg.Modules, wikiModuleCfg{Name: name, Description: desc})
}

// cfgSetTableAlias 同步更新 cfg.Tables（渲染用）。
func cfgSetTableAlias(cfg *wikiConfig, name, alias string) {
	for i := range cfg.Tables {
		if cfg.Tables[i].Name == name {
			cfg.Tables[i].Alias = alias
			return
		}
	}
	cfg.Tables = append(cfg.Tables, wikiTableConfig{Name: name, Alias: alias})
}

// cfgSetColumnComments 同步更新 cfg.Tables 的列说明（渲染用）。
func cfgSetColumnComments(cfg *wikiConfig, tbl string, comments map[string]string) {
	ti := -1
	for i := range cfg.Tables {
		if cfg.Tables[i].Name == tbl {
			ti = i
			break
		}
	}
	if ti < 0 {
		cfg.Tables = append(cfg.Tables, wikiTableConfig{Name: tbl})
		ti = len(cfg.Tables) - 1
	}
	for col, comment := range comments {
		found := false
		for j := range cfg.Tables[ti].Columns {
			if cfg.Tables[ti].Columns[j].Name == col {
				cfg.Tables[ti].Columns[j].Comment = comment
				found = true
				break
			}
		}
		if !found {
			cfg.Tables[ti].Columns = append(cfg.Tables[ti].Columns, wikiTableColumn{Name: col, Comment: comment})
		}
	}
}
