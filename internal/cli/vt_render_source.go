package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// sourceLineCache 文件 → 行数组缓存（渲染一次查询读多次同文件）。
type sourceLineCache struct {
	repoDir string
	files   map[string][]string
}

func newSourceLineCache(repoDir string) *sourceLineCache {
	return &sourceLineCache{repoDir: repoDir, files: map[string][]string{}}
}

// line 返回文件第 n 行（1 基）内容（截断 60 字符，去首尾空白）；
// 读不到返回空。
func (c *sourceLineCache) line(filePath string, n int) string {
	lines, ok := c.files[filePath]
	if !ok {
		p := filePath
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.repoDir, filePath)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			c.files[filePath] = nil
			return ""
		}
		lines = strings.Split(string(b), "\n")
		c.files[filePath] = lines
	}
	if n <= 0 || n > len(lines) {
		return ""
	}
	s := strings.TrimSpace(lines[n-1])
	if len(s) > 60 {
		s = s[:60] + "..."
	}
	return s
}

// shortAnchor 短锚点：函数短名#节点名（不含完整 canonical ID）。
func shortAnchor(r *domain.TraceRow) string {
	fn := shortFuncName(r.FuncID)
	if fn == "" {
		fn = "（未知函数）"
	}
	return fn + "#" + r.Name
}

// splitAssign 解析锚点行等号：返回左边路径基址与右边首个标识符。
//
//	"u.Brands = brands" → ("u", "brands")；无等号 → ("", "")。
//
// Q236 扩展：无等号时的复合字面量键值对（§71/§73 遗留）——
//
//	"Email: req.Email," → ("", "req.Email")：冒号右侧是写入值来源
//
// （去尾逗号，完整表达式匹配节点 instance_path），左侧是写入字段名
// （非对象基址，对象在字面量赋值目标处，行内取不到）。
func splitAssign(line string) (string, string) {
	idx := strings.Index(line, ":=")
	if idx < 0 {
		idx = strings.Index(line, "=")
	}
	if idx < 0 {
		if i := strings.Index(line, ":"); i >= 0 {
			rv := strings.TrimSpace(line[i+1:])
			if j := strings.Index(rv, "//"); j >= 0 {
				rv = rv[:j]
			}
			rv = strings.TrimSuffix(strings.TrimSpace(rv), ",")
			return "", rv
		}
		return "", ""
	}
	left := strings.TrimSpace(line[:idx])
	right := strings.TrimSpace(line[idx+2:])

	base := left
	if i := strings.LastIndex(left, "."); i >= 0 {
		base = left[:i]
	}

	rv := right
	if i := strings.IndexAny(rv, ".( "); i >= 0 {
		rv = rv[:i]
	}
	return strings.TrimSpace(base), strings.TrimSpace(rv)
}

// receiverSource 方法调用接收者补充（Q235-12）：节点源码行若为方法
// 调用赋值（u := svc.GetOrm()）——返回接收者名与其定义行（svc :=
// &Svc{}）。无方法调用/找不到定义返回空。
func receiverSource(cache *sourceLineCache, filePath string, line int) (name string, defLine int, defSrc string) {
	src := cache.line(filePath, line)
	if src == "" {
		return "", 0, ""
	}

	idx := strings.Index(src, ":=")
	if idx < 0 {
		idx = strings.Index(src, "=")
	}
	if idx < 0 {
		return "", 0, ""
	}
	rhs := strings.TrimSpace(src[idx+2:])
	if i := strings.Index(rhs, "."); i <= 0 {
		return "", 0, ""
	} else {
		rhs = rhs[:i]
	}
	recv := strings.TrimSpace(rhs)
	if recv == "" {
		return "", 0, ""
	}

	lines, ok := cache.files[filePath]
	if !ok {
		return recv, 0, ""
	}
	for l := line - 2; l >= 0; l-- {
		text := strings.TrimSpace(lines[l])
		if strings.HasPrefix(text, recv+" :=") || strings.HasPrefix(text, recv+" =") ||
			strings.HasPrefix(text, "var "+recv+" ") || text == "var "+recv {
			return recv, l + 1, text
		}
	}
	return recv, 0, ""
}

// classifySource 来源侧（dir=0）节点分组：
// depth==1 且命中锚点行等号右边 → 写入值；命中左边基址 → 对象；
// 其余（更深来源）→ 来源。
func classifySource(r *domain.TraceRow, leftBase, rightVar string) vtGroup {
	if r.Depth == 1 {
		if rightVar != "" && r.Name == rightVar {
			return vtWriteValue
		}
		if leftBase != "" && r.Name == leftBase {
			return vtObject
		}
	}
	return vtSource
}
