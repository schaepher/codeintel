package sqlite

// P1-05：全局注册表（~/.codeintel/codeintel.db）的建表语句作为仓库外
// 表的事实源——repos 表的列类型在仓库内没有 schema 事实源（R19 遗留
// 10 列无类型）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegistryTableSchemas 读取注册表 sqlite_master（repos 表 DDL 含列类型）。
func TestRegistryTableSchemas(t *testing.T) {
	dir := t.TempDir()
	reg, err := OpenRegistry(dir)
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	reg.Close()

	ddls, err := RegistryTableSchemas(dir)
	if err != nil {
		t.Fatalf("RegistryTableSchemas: %v", err)
	}
	ddl, ok := ddls["repos"]
	if !ok {
		t.Fatalf("应有 repos 表 DDL，得到 %d 张表", len(ddls))
	}
	for _, want := range []string{"path", "TEXT", "go_mod_count", "INTEGER", "registered_at"} {
		if !strings.Contains(ddl, want) {
			t.Errorf("repos DDL 缺 %q:\n%s", want, ddl)
		}
	}
}

// TestRegistryTableSchemasMissingDir 注册表目录/库不存在时返回空且不建库
// （wiki 生成不能因为没注册过仓库就失败或留下空文件）。
func TestRegistryTableSchemasMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	ddls, err := RegistryTableSchemas(dir)
	if err != nil {
		t.Fatalf("缺失目录应返回空而非报错：%v", err)
	}
	if len(ddls) != 0 {
		t.Errorf("缺失目录应返回空 map，得到 %v", ddls)
	}
	if _, err := os.Stat(filepath.Join(dir, "codeintel.db")); !os.IsNotExist(err) {
		t.Errorf("不应创建数据库文件（stat err=%v）", err)
	}
}
