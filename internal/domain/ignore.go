package domain

import "strings"

// IsIgnoredPath 判断仓库相对路径 rel 是否落在忽略目录（或子目录）内。
// ignore 元素为仓库相对目录（如 "vendor"、"third_party/gen"）；前导
// "./"、末尾 "/" 与 Windows 反斜杠会被归一。rel 也接受同样的归一。
func IsIgnoredPath(rel string, ignore []string) bool {
	if len(ignore) == 0 || rel == "" {
		return false
	}
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	for _, d := range ignore {
		d = strings.Trim(strings.TrimPrefix(strings.ReplaceAll(d, "\\", "/"), "./"), "/")
		if d == "" || d == "." {
			continue
		}
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}
