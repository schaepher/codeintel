package action

// R29 grpc 枚举支持（待办 6）：.proto 源文件 enum 块提取——proto 是
// 权威值来源（枚举名 + 值名 + 值号 + 文件:行）。轻量文本解析（不调
// protoc）：识别 enum 块、值行 IDENT = NUMBER、注释（// 与 /** */
// 上一行、尾注释优先）、嵌套 message 前缀（Outer.Enum）。

import (
	"os"
	"path/filepath"
	"strings"
)

// extractProtoEnums 提取仓库内 .proto 源文件枚举（Source="proto"）。
// 返回 []EnumEntry：Type=枚举名（嵌套带前缀）、Group=枚举名、
// Name=值名、Value=值号（字符串）、Comment=注释、Pkg=proto package。
func extractProtoEnums(repoAbs string) []EnumEntry {
	var out []EnumEntry
	_ = filepath.WalkDir(repoAbs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".proto") {
			return nil
		}
		if strings.Contains(path, "/vendor/") || strings.Contains(path, ".codeintel") {
			return nil
		}
		out = append(out, scanProtoEnums(path, repoAbs)...)
		return nil
	})
	return out
}

// scanProtoEnums 单文件 enum 扫描：词法状态机——注释/字符串跳过、
// 括号栈跟踪 message 嵌套、enum 块内识别值行。
func scanProtoEnums(path, repoAbs string) []EnumEntry {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := string(src)
	var out []EnumEntry
	var stack []string // message/service 嵌套名（枚举名前缀）
	inEnum := false
	enumName := ""
	pending := "" // 值上一行的注释
	line := 1
	pkg := protoPackage(s)
	if pkg == "" {
		pkg = filepath.Base(filepath.Dir(path)) // 无 package 声明 → 目录名
	}

	for i := 0; i < len(s); {
		c := s[i]
		// 行注释
		if c == '/' && i+1 < len(s) && s[i+1] == '/' {
			end := i
			for end < len(s) && s[end] != '\n' {
				end++
			}
			if inEnum {
				if cm := strings.TrimSpace(s[i+2 : end]); cm != "" {
					pending = cm
				}
			}
			i = end
			continue
		}
		// 块注释（/** doc */ 单行可作值注释）
		if c == '/' && i+1 < len(s) && s[i+1] == '*' {
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				break
			}
			seg := s[i : i+end+4]
			line += strings.Count(seg, "\n")
			if inEnum && !strings.Contains(seg, "\n") {
				cm := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(seg, "/*"), "*/"))
				cm = strings.TrimSpace(strings.TrimPrefix(cm, "*")) // /** doc */ 多余星
				if cm != "" {
					pending = cm
				}
			}
			i += end + 4
			continue
		}
		// 字符串字面量
		if c == '"' || c == '\'' {
			i++
			for i < len(s) && s[i] != c && s[i] != '\n' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
			i++
			continue
		}
		// 标识符
		if isProtoIdentByte(c) {
			start := i
			for i < len(s) && isProtoIdentByte(s[i]) {
				i++
			}
			word := s[start:i]
			switch word {
			case "enum":
				name := skipToIdent(s, i)
				enumName = name
				if len(stack) > 0 {
					enumName = strings.Join(stack, ".") + "." + name
				}
				inEnum = true
				pending = ""
			case "message", "service":
				stack = append(stack, skipToIdent(s, i))
			case "option", "reserved":
				// enum 体内关键字——跳行（不解析为值）
				i = skipToLineEnd(s, i, &line)
				continue
			default:
				if inEnum {
					// 值行：IDENT = NUMBER [option] ; [// 注释]
					if eq := findByte(s, i, '='); eq >= 0 {
						num := skipSpaces(s, eq+1)
						if num < len(s) && (s[num] == '-' || s[num] == '+') {
							num++
						}
						numStart := num
						for num < len(s) && s[num] >= '0' && s[num] <= '9' {
							num++
						}
						comment := pending
						if tc := lineTailComment(s, num); tc != "" {
							comment = tc
						}
						out = append(out, EnumEntry{
							Pkg: pkg, Type: enumName, Group: enumName,
							Name: word, Value: s[numStart:num], Comment: comment,
							File: filepath.ToSlash(strings.TrimPrefix(path, repoAbs+string(filepath.Separator))),
							Line: line, Source: "proto",
						})
					}
				}
				i = skipToLineEnd(s, i, &line)
				continue
			}
			continue
		}
		if c == '}' {
			if inEnum {
				inEnum = false
				enumName = ""
			} else if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
		if c == '\n' {
			line++
		}
		i++
	}
	return out
}

// protoPackage 提取文件头 package 声明（无则空）。
func protoPackage(s string) string {
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "package") && strings.HasSuffix(t, ";") {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(t, "package"), ";"))
		}
	}
	return ""
}

// skipToIdent 跳过空白取下一个标识符（enum/message 名）。
func skipToIdent(s string, i int) string {
	for i < len(s) && !isProtoIdentByte(s[i]) {
		i++
	}
	start := i
	for i < len(s) && isProtoIdentByte(s[i]) {
		i++
	}
	return s[start:i]
}

// skipToLineEnd 跳到行尾（值行消费；消费 \n 计入行号）。
func skipToLineEnd(s string, i int, line *int) int {
	for i < len(s) {
		if s[i] == '\n' {
			*line++
			return i + 1
		}
		i++
	}
	return i
}

// findByte 找下一个指定字节（限行内）。
func findByte(s string, i int, b byte) int {
	for i < len(s) && s[i] != b && s[i] != '\n' {
		i++
	}
	if i < len(s) && s[i] == b {
		return i
	}
	return -1
}

// skipSpaces 跳过空格/制表符。
func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// lineTailComment 值行尾注释（; 后的 //）——优先于上一行注释。
func lineTailComment(s string, i int) string {
	for i < len(s) && s[i] != '\n' && s[i] != ';' {
		i++
	}
	if i < len(s) && s[i] == ';' {
		j := skipSpaces(s, i+1)
		if j+1 < len(s) && s[j] == '/' && s[j+1] == '/' {
			end := j + 2
			for end < len(s) && s[end] != '\n' {
				end++
			}
			return strings.TrimSpace(s[j+2 : end])
		}
	}
	return ""
}

// isProtoIdentByte proto 标识符字符（字母数字下划线）。
func isProtoIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
