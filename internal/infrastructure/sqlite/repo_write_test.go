package sqlite

import (
	"errors"
	"testing"
)

// TestIsFKError 驱动无关 FK 错误判定（#227 release 交叉编译暴露：
// mattn/go-sqlite3 的 sqlite3.Error 类型在 CGO_ENABLED=0 时不存在，
// 改用 SQLite 官方报错文案匹配——mattn 与 modernc 输出一致）。
func TestIsFKError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"FK 文案直接命中", errors.New("FOREIGN KEY constraint failed"), true},
		{"FK 文案带上下文", errors.New("FOREIGN KEY constraint failed: table_a.a_id"), true},
		{"errors 包装后仍命中", errors.Join(errors.New("db write"), errors.New("FOREIGN KEY constraint failed")), true},
		{"非 FK 文案", errors.New("UNIQUE constraint failed: nodes.id"), false},
		{"无关错误", errors.New("database is locked"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isFKError(c.err); got != c.want {
				t.Errorf("isFKError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
