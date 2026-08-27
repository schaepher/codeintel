package cli

// S7 `codeintel config merge`——检查现有全局配置缺失的配置项，从
// 内置模板补默认值（保留用户已有的值/注释——只追加缺失项）。
// Makefile install 在配置已存在时调用（R60 只处理"不存在则初始化"，
// 新增配置项后旧配置缺项——S7 补上）。

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// cmdConfigMerge 读 ~/.codeintel/config.yaml，与内置模板对比缺失项
// 补默认（递归——顶层键与嵌套键都查）。返回 0 成功 / 1 失败。
func cmdConfigMerge() int {
	path := agentConfigPath()
	if path == "" {
		fmt.Fprintln(os.Stderr, "error: 无法定位全局配置路径")
		return 1
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: 读取 %s: %v（先 `codeintel config default > %s` 初始化）\n", path, err, path)
		return 1
	}

	// 模板与现有配置都解析为嵌套 map——递归 diff 缺失键路径
	var tpl map[string]any
	if err := yaml.Unmarshal([]byte(configExample), &tpl); err != nil {
		fmt.Fprintf(os.Stderr, "error: 模板解析: %v\n", err)
		return 1
	}
	var cur map[string]any
	if err := yaml.Unmarshal(existing, &cur); err != nil {
		fmt.Fprintf(os.Stderr, "error: 现有配置解析 %s: %v（YAML 语法错误需人工修复）\n", path, err)
		return 1
	}
	var missing [][]string
	diffKeys("", tpl, cur, &missing)
	if len(missing) == 0 {
		fmt.Println("配置无缺失项")
		return 0
	}

	// 从模板原始文本提取缺失键的行组（含注释），插入现有文件对应位置
	lines := strings.Split(string(existing), "\n")
	inserted := 0
	for _, path := range missing {
		block := extractTemplateBlock(configExample, path)
		if block == "" {
			continue
		}
		lines = insertBlock(lines, path, block)
		inserted++
	}
	if inserted == 0 {
		fmt.Fprintln(os.Stderr, "error: 模板提取失败（无缺失项可补）")
		return 1
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: 写回 %s: %v\n", path, err)
		return 1
	}
	names := make([]string, 0, len(missing))
	for _, p := range missing {
		names = append(names, strings.Join(p, "."))
	}
	fmt.Printf("已补缺 %d 项配置（默认值）：%s\n", inserted, strings.Join(names, ", "))
	return 0
}

// diffKeys 递归对比模板与现有配置——缺失键路径收集（如 ["seq","filter_pkgs"]）。
func diffKeys(prefix string, tpl, cur map[string]any, missing *[][]string) {
	var keys []string
	for k := range tpl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		tv := tpl[k]
		cv, ok := cur[k]
		if !ok {
			// 路径拆分（如 "seq.filter_pkgs" → ["seq","filter_pkgs"]——
			// extractTemplateBlock 按路径逐层定位）
			p := strings.TrimPrefix(prefix+"."+k, ".")
			*missing = append(*missing, strings.Split(p, "."))
			continue
		}
		tm, tok := tv.(map[string]any)
		cm, cok := cv.(map[string]any)
		if tok && cok {
			diffKeys(prefix+"."+k, tm, cm, missing)
		}
	}
}

// extractTemplateBlock 从模板原始文本提取键路径对应的行组（含前导
// 注释与缩进内容——保持模板的默认值与注释）。
func extractTemplateBlock(tmpl string, keyPath []string) string {
	lines := strings.Split(tmpl, "\n")
	// 定位顶层键行
	top := keyPath[0]
	start := -1
	commentStart := -1
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "#") || trim == "" {
			continue
		}
		if !strings.HasPrefix(trim, top+":") {
			continue
		}
		// 确认是顶层键（无缩进）
		if l != "" && (l[0] == ' ' || l[0] == '\t') {
			continue
		}
		start = i
		// 前导注释块起点
		commentStart = i
		for commentStart > 0 && strings.HasPrefix(strings.TrimSpace(lines[commentStart-1]), "#") {
			commentStart--
		}
		break
	}
	if start < 0 {
		return ""
	}
	// 顶层块结束 = 下一个顶层键
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if l == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' {
			end = i
			break
		}
	}
	block := strings.Join(lines[start:end], "\n")
	if len(keyPath) == 1 {
		// 含前导注释
		if commentStart >= 0 {
			block = strings.Join(lines[commentStart:end], "\n")
		}
		return block
	}
	// 嵌套键：在顶层块内定位子键行组（含前导注释）
	sub := keyPath[1]
	subIndent := -1
	subStart := -1
	for i, l := range lines[start:end] {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, sub+":") && countIndent(l) > 0 {
			subIndent = countIndent(l)
			subStart = i
			break
		}
	}
	_ = subIndent
	if subStart < 0 {
		return ""
	}
	// 含前导注释（紧邻的 # 行）
	subCommentStart := subStart
	for subCommentStart > 0 && strings.HasPrefix(strings.TrimSpace(lines[start+subCommentStart-1]), "#") {
		subCommentStart--
	}
	// 结束 = 下一个缩进 <= subIndent 的键行（或顶层块尾）——相对索引
	subEnd := end - start
	for i := subStart + 1; i < end-start; i++ {
		t := strings.TrimSpace(lines[start+i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if countIndent(lines[start+i]) <= subIndent {
			subEnd = i
			break
		}
	}
	return strings.Join(lines[start+subCommentStart:start+subEnd], "\n")
}

// countIndent 行前导缩进字符数。
func countIndent(l string) int {
	n := 0
	for _, c := range l {
		if c == ' ' || c == '\t' {
			n++
		} else {
			break
		}
	}
	return n
}

// insertBlock 把模板块插入现有配置：顶层键缺失 → 文件尾追加；嵌套
// 键缺失 → 插入父键块末尾（父键最后内容行后）。
func insertBlock(lines []string, keyPath []string, block string) []string {
	if len(keyPath) == 1 {
		// 顶层键：文件尾追加（前空一行）
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		return append(lines, strings.Split(block, "\n")...)
	}
	// 嵌套键：定位父键块末尾（父键下最深内容行后、下一个同级键前）
	parent := keyPath[0]
	parentEnd := -1
	parentIndent := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, parent+":") && (i == 0 || (lines[i][0] != ' ' && lines[i][0] != '\t')) {
			parentIndent = countIndent(l)
			for j := i + 1; j < len(lines); j++ {
				tj := strings.TrimSpace(lines[j])
				if tj == "" || strings.HasPrefix(tj, "#") {
					continue
				}
				if lines[j][0] != ' ' && lines[j][0] != '\t' {
					parentEnd = j
					break
				}
				parentEnd = j + 1
			}
			break
		}
	}
	_ = parentIndent
	if parentEnd < 0 {
		return lines
	}
	out := make([]string, 0, len(lines)+4)
	out = append(out, lines[:parentEnd]...)
	out = append(out, strings.Split(block, "\n")...)
	out = append(out, lines[parentEnd:]...)
	return out
}
