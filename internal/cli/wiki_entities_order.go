package cli

import (
	"github.com/schaepher/codeintel/internal/domain"
)

// chainLineNum 调用边行号（metadata.line_num——AST 发射时记录调用
// 位置；缺失返回 -1）。
func chainLineNum(f *domain.Fact) int {
	if v, ok := f.Metadata["line_num"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
	}
	return -1
}
