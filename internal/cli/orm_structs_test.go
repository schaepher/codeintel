package cli

// R20 表关联结构体扫描测试：TableName() 方法反查结构体 + 源码片段
// 提取——表定义上方展示可折叠核对。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ormStructFixture 建含 ORM 结构体的临时仓库。
func ormStructFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/orm\n\ngo 1.21\n")
	write("model.go", `package orm

import "time"

// Order 订单
type Order struct {
	ID        int64     `+"`gorm:\"primaryKey\"`"+`
	OrderNo   string    `+"`gorm:\"size:32\"`"+`
	CreatedAt time.Time `+"`gorm:\"autoCreateTime\"`"+`
}

func (Order) TableName() string { return "order_tab" }

type User struct {
	ID   int64
	Name string
}

func (User) TableName() string { return "user_tab" }
`)
	write("plain.go", `package orm

// Plain 无 TableName——不关联表
type Plain struct {
	X int
}
`)
	return dir
}

// TestScanORMStructs：TableName 反查结构体 + 源码片段。
func TestScanORMStructs(t *testing.T) {
	dir := ormStructFixture(t)
	got := scanORMStructs(dir)
	orders := got["order_tab"]
	if len(orders) != 1 || orders[0].Name != "Order" {
		t.Fatalf("order_tab = %+v, want Order", orders)
	}
	if !strings.Contains(orders[0].Code, "OrderNo") || !strings.Contains(orders[0].Code, "CreatedAt") {
		t.Errorf("Order 源码片段应含字段:\n%s", orders[0].Code)
	}
	if users := got["user_tab"]; len(users) != 1 || users[0].Name != "User" {
		t.Errorf("user_tab = %+v, want User", users)
	}
	if _, ok := got["plain"]; ok {
		t.Error("无 TableName 的结构体不应关联表")
	}
	_ = filepath.Join
}
