// U05：SQLite 文件 URI 完整路径——#、?、%、空格不得被当成片段/参数/转义；
// 库必须落在各自 .codeintel/codeintel.db，互不串库，根下不得出现截断路径。
package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openIsolated 在 root 下创建 name 目录并打开其仓库库。
func openIsolated(t *testing.T, root, name string) (*DB, string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	return db, dir
}

func TestFileURIPathEncoding(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"hash", "repo#one"},
		{"question", "repo?two"},
		{"percent", "50%off"},
		{"space", "repo with space"},
		{"hash00", "repo#00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			db, dir := openIsolated(t, root, tc.path)
			if _, err := db.Exec("CREATE TABLE uri_probe(value TEXT)"); err != nil {
				t.Fatalf("create: %v", err)
			}
			if _, err := db.Exec("INSERT INTO uri_probe VALUES(?)", tc.name); err != nil {
				t.Fatalf("insert: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			// 库文件真实落在 .codeintel/codeintel.db
			dbPath := filepath.Join(dir, ".codeintel", "codeintel.db")
			if _, err := os.Stat(dbPath); err != nil {
				t.Fatalf("expected db at %s: %v", dbPath, err)
			}
			// 根下不得出现截断的（# 之前）残留路径
			truncated := strings.SplitN(filepath.Base(dir), "#", 2)[0]
			if truncated != filepath.Base(dir) {
				if _, err := os.Stat(filepath.Join(root, truncated)); err == nil {
					t.Fatalf("truncated path %q exists at repo root", truncated)
				}
			}
			// 连续重开：数据可见且参数保持
			db2, err := Open(dir)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer db2.Close()
			var got string
			if err := db2.QueryRow("SELECT value FROM uri_probe").Scan(&got); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got != tc.name {
				t.Fatalf("value = %q, want %q", got, tc.name)
			}
			var mode string
			if err := db2.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
				t.Fatalf("journal_mode: %v", err)
			}
			if !strings.EqualFold(mode, "wal") {
				t.Fatalf("journal_mode = %q, want wal", mode)
			}
			var fk int
			if err := db2.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
				t.Fatalf("foreign_keys: %v", err)
			}
			if fk != 1 {
				t.Fatalf("foreign_keys = %d, want 1", fk)
			}
			var busy int
			if err := db2.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
				t.Fatalf("busy_timeout: %v", err)
			}
			if busy != 5000 {
				t.Fatalf("busy_timeout = %d, want 5000", busy)
			}
		})
	}
}

// 两个 # 路径必须是两个独立库：不能互相读到节点。
func TestFileURIDistinctReposNoLeak(t *testing.T) {
	root := t.TempDir()
	one, oneDir := openIsolated(t, root, "repo#one")
	if _, err := one.Exec("CREATE TABLE uri_probe(value TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := one.Exec("INSERT INTO uri_probe VALUES('one')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	two, twoDir := openIsolated(t, root, "repo#two")
	defer two.Close()
	defer one.Close()

	var n int
	// 表不存在或 0 行都算不串库；关键是绝不能读到 one 的行。
	err := two.QueryRow("SELECT count(*) FROM uri_probe WHERE value='one'").Scan(&n)
	if err == nil && n > 0 {
		t.Fatalf("cross repo leak: two saw %d rows from one", n)
	}
	if oneDir == twoDir {
		t.Fatalf("repos share dir %s", oneDir)
	}
}
