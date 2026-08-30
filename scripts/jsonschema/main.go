package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
)

var numericRegex = regexp.MustCompile(`^[0-9]+$`)

// 全局存储
var (
	typeMap     = make(map[string]string)          // 路径 -> 类型
	freqMap     = make(map[string]map[string]int) // 路径 -> 值 -> 出现次数（仅基本类型）
	enumEnabled bool
	maxStrLen   int // 单个字符串长度阈值
	maxEnumCnt  int // 枚举值最大数量阈值
)

func main() {
	// 命令行参数
	var (
		inputFile    = flag.String("f", "", "输入 JSON 文件路径（必填）")
		mode         = flag.String("mode", "jsonl", "输入格式: jsonl（每行一个 JSON） 或 array（顶级数组）")
		outputFile   = flag.String("o", "", "输出文件（可选，默认输出到 stdout）")
		format       = flag.String("format", "json", "输出格式: json（JSON对象） 或 text（每行 路径=类型）")
		enableEnum   = flag.Bool("enum", false, "是否启用枚举值收集（默认关闭）")
		strLenThresh = flag.Int("max-enum-string-len", 20, "字符串枚举单个值的最大长度，超过则排除")
		countThresh  = flag.Int("max-enum-count", 30, "枚举值去重后的最大数量，超过则排除")
	)
	flag.Parse()

	if *inputFile == "" {
		log.Fatal("请指定输入文件: -f <path>")
	}

	enumEnabled = *enableEnum
	maxStrLen = *strLenThresh
	maxEnumCnt = *countThresh

	file, err := os.Open(*inputFile)
	if err != nil {
		log.Fatalf("打开文件失败: %v", err)
	}
	defer file.Close()

	switch *mode {
	case "jsonl":
		processJSONL(file)
	case "array":
		processArray(file)
	default:
		log.Fatalf("未知模式: %s (支持 jsonl 或 array)", *mode)
	}

	out := os.Stdout
	if *outputFile != "" {
		out, err = os.Create(*outputFile)
		if err != nil {
			log.Fatalf("创建输出文件失败: %v", err)
		}
		defer out.Close()
	}

	switch *format {
	case "json":
		outputJSON(out)
	case "text":
		outputText(out)
	default:
		log.Fatalf("未知输出格式: %s (支持 json 或 text)", *format)
	}
}

func collectTypes(path string, value interface{}) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, val := range v {
			var segment string
			if numericRegex.MatchString(key) {
				segment = "[]"
			} else {
				segment = key
			}
			newPath := segment
			if path != "" {
				newPath = path + "." + segment
			}
			collectTypes(newPath, val)
		}
	case []interface{}:
		elemPath := path + "[]"
		// 过滤掉 nil 元素
		nonNilElems := make([]interface{}, 0, len(v))
		for _, elem := range v {
			if elem != nil {
				nonNilElems = append(nonNilElems, elem)
			}
		}
		if len(nonNilElems) == 0 {
			// 所有元素都是 nil，视为空数组，不记录
			return
		}
		// 处理非 nil 元素
		for _, elem := range nonNilElems {
			switch e := elem.(type) {
			case map[string]interface{}:
				recordTypeAndValue(elemPath, "object", nil)
				collectTypes(elemPath, e)
			case []interface{}:
				recordTypeAndValue(elemPath, "array", nil)
				collectTypes(elemPath, e)
			default:
				// 基本类型，由 collectTypes 处理（不会为 nil）
				collectTypes(elemPath, e)
			}
		}
	default:
		// 忽略 null 值
		if v == nil {
			return
		}
		typ := fmt.Sprintf("%T", v)
		recordTypeAndValue(path, typ, v)
	}
}

// recordTypeAndValue 记录路径的类型，并记录基本类型的值出现次数
// 对于 "object" 和 "array" 类型，不进行枚举收集。
func recordTypeAndValue(path, typ string, value interface{}) {
	if path == "" {
		return
	}

	// 处理类型冲突
	if existing, ok := typeMap[path]; ok {
		if existing == "empty_array" && typ != "empty_array" {
			typeMap[path] = typ
			delete(freqMap, path)
		} else if existing != typ {
			typeMap[path] = "mixed"
			delete(freqMap, path) // 冲突时清除枚举频次
			return
		}
		// 类型相同，继续
	} else {
		typeMap[path] = typ
	}

	// 非基本类型（object/array）不进行枚举收集
	if typ == "empty_array" || typ == "mixed" || typ == "object" || typ == "array" {
		return
	}
	// 如果 value 是对象或数组（不应该发生，但安全处理）
	if _, ok := value.(map[string]interface{}); ok {
		return
	}
	if _, ok := value.([]interface{}); ok {
		return
	}

	// 基本类型：记录值频次（用于枚举判定）
	if enumEnabled {
		strVal := fmt.Sprintf("%v", value)
		if freqMap[path] == nil {
			freqMap[path] = make(map[string]int)
		}
		freqMap[path][strVal]++
	}
}

// isEnum 判定路径是否为枚举
// 规则：排除类型为 mixed/object/array/empty_array；检查长度和数量；重复优先
func isEnum(path string) bool {
	if !enumEnabled {
		return false
	}
	typ, ok := typeMap[path]
	if !ok || typ == "mixed" || typ == "empty_array" || typ == "object" || typ == "array" {
		return false
	}
	freq, ok := freqMap[path]
	if !ok || len(freq) == 0 {
		return false
	}

	// 1. 检查单个字符串长度（仅对 string 类型）
	if typ == "string" {
		for val := range freq {
			if len(val) > maxStrLen {
				return false
			}
		}
	}

	// 2. 检查去重后数量是否超限
	if len(freq) > maxEnumCnt {
		return false
	}

	// 3. 检查是否有重复值
	for _, cnt := range freq {
		if cnt >= 2 {
			return true
		}
	}

	// 4. 对于 string 类型，检查所有不同值的总字符数
	if typ == "string" {
		totalLen := 0
		for val := range freq {
			totalLen += len(val)
		}
		if totalLen <= maxStrLen {
			return true
		}
	}
	return false
}

// getEnumValues 返回路径的枚举值列表（去重排序）
func getEnumValues(path string) []string {
	freq, ok := freqMap[path]
	if !ok {
		return nil
	}
	vals := make([]string, 0, len(freq))
	for v := range freq {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	return vals
}

// outputJSON 输出 JSON 格式
func outputJSON(w *os.File) {
	result := make(map[string]interface{})
	for path, typ := range typeMap {
		entry := map[string]interface{}{"type": typ}
		if isEnum(path) {
			entry["enum"] = getEnumValues(path)
		}
		result[path] = entry
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		log.Fatalf("输出 JSON 失败: %v", err)
	}
}

// outputText 输出文本格式
func outputText(w *os.File) {
	paths := make([]string, 0, len(typeMap))
	for p := range typeMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		typ := typeMap[path]
		line := path + "=" + typ
		if isEnum(path) {
			line += " enum=[" + strings.Join(getEnumValues(path), ", ") + "]"
		}
		fmt.Fprintln(w, line)
	}
}