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
// 注释与缩进内容——保持模板的默认值与注释）。任意深度（R100：3 层
// 以上如 ai.fill.modules——原实现硬编码 2 层，3 层会整块返回）。
func extractTemplateBlock(tmpl string, keyPath []string) string {
	lines := strings.Split(tmpl, "\n")
	// 第一层：定位顶层键行（无缩进）
	start := -1
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if trim == "" || strings.HasPrefix(trim, "#") || !strings.HasPrefix(trim, keyPath[0]+":") {
			continue
		}
		if countIndent(l) == 0 {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	// 第一层块结束 = 下一个顶层键（缩进 0）
	topEnd := len(lines)
	for j := start + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if countIndent(lines[j]) == 0 {
			topEnd = j
			break
		}
	}
	// 逐层下钻：每层在父块内找 keyPath[i] 子键行（缩进 > 父缩进），
	// 块边界 = 下一个缩进 <= 当前层缩进的键行
	curStart, curEnd, curIndent := start, topEnd, 0
	for i := 1; i < len(keyPath); i++ {
		key := keyPath[i]
		subStart, subIndent := -1, -1
		for j := curStart + 1; j < curEnd; j++ {
			l := lines[j]
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			ind := countIndent(l)
			if ind <= curIndent {
				break // 已出父块
			}
			if strings.HasPrefix(t, key+":") {
				subStart, subIndent = j, ind
				break
			}
		}
		if subStart < 0 {
			return ""
		}
		subEnd := curEnd
		for j := subStart + 1; j < curEnd; j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if countIndent(lines[j]) <= subIndent {
				subEnd = j
				break
			}
		}
		curStart, curEnd, curIndent = subStart, subEnd, subIndent
	}
	// 前导注释块（最近一层键的紧邻 # 行）
	commentStart := curStart
	for commentStart > 0 && strings.HasPrefix(strings.TrimSpace(lines[commentStart-1]), "#") {
		commentStart--
	}
	return strings.Join(lines[commentStart:curEnd], "\n")
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
// 键缺失 → 插入父键块末尾（父键最后内容行后）。任意深度（R100：3
// 层以上逐层下钻定位插入点——原实现只按 keyPath[0] 找顶层块）。
func insertBlock(lines []string, keyPath []string, block string) []string {
	if len(keyPath) == 1 {
		// 顶层键：文件尾追加（前空一行）
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		return append(lines, strings.Split(block, "\n")...)
	}
	// 逐层下钻定位最深层父键行（i==0 顶层缩进 0；i>0 缩进 > 父缩进）
	curStart, curEnd, curIndent := -1, len(lines), -1
	for i := 0; i < len(keyPath)-1; i++ {
		key := keyPath[i]
		next := -1
		for j := curStart + 1; j < curEnd; j++ {
			l := lines[j]
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			ind := countIndent(l)
			if i > 0 && ind <= curIndent {
				break // 已出父块
			}
			if strings.HasPrefix(t, key+":") && ((i == 0 && ind == 0) || (i > 0 && ind > curIndent)) {
				next = j
				break
			}
		}
		if next < 0 {
			return lines
		}
		curStart, curIndent = next, countIndent(lines[next])
	}
	// 父块末尾：父键行后最深内容行后、下一个缩进 <= 父缩进的键行前
	parentEnd := len(lines)
	for j := curStart + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if countIndent(lines[j]) <= curIndent {
			parentEnd = j
			break
		}
		parentEnd = j + 1
	}
	out := make([]string, 0, len(lines)+4)
	out = append(out, lines[:parentEnd]...)
	out = append(out, strings.Split(block, "\n")...)
	out = append(out, lines[parentEnd:]...)
	return out
}
