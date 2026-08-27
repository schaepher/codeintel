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
	ID        int64     `+"`gorm:\"column:order_id;primaryKey;autoIncrement\"`"+`
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
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := acts.ORMStructs(dir)
	if err != nil {
		t.Fatal(err)
	}
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

// TestORMColTypes：结构体字段 Go 类型 → 表列 fallback
// （gorm column tag 优先、无 tag snake_case）。
func TestORMColTypes(t *testing.T) {
	dir := ormStructFixture(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	ormStructs, _ := acts.ORMStructs(dir)
	got := ormColTypes(ormStructs)
	// order_tab：ID → column:order_id（tag 优先）+ int64
	if typ := got["order_tab"]["order_id"]; typ != "int64" {
		t.Errorf("order_tab.order_id = %q, want int64（gorm column tag）", typ)
	}
	// OrderNo → snake_case order_no + string
	if typ := got["order_tab"]["order_no"]; typ != "string" {
		t.Errorf("order_tab.order_no = %q, want string（snake_case）", typ)
	}
	if typ := got["order_tab"]["created_at"]; typ != "time.Time" {
		t.Errorf("order_tab.created_at = %q, want time.Time", typ)
	}
	// user_tab：ID → id（snake_case）
	if typ := got["user_tab"]["id"]; typ != "int64" {
		t.Errorf("user_tab.id = %q, want int64", typ)
	}
}

// TestORMFieldOrderAndAutoInc：字段顺序还原结构体序 + 自增列第一。
func TestORMFieldOrderAndAutoInc(t *testing.T) {
	dir := ormStructFixture(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	ormStructs, _ := acts.ORMStructs(dir)
	order := ormColOrder(ormStructs)
	autoInc := ormAutoIncCols(ormStructs)
	oi, ok1 := order["order_tab"]["order_id"]
	on, ok2 := order["order_tab"]["order_no"]
	ca, ok3 := order["order_tab"]["created_at"]
	if !ok1 || !ok2 || !ok3 || oi >= on || on >= ca {
		t.Errorf("结构体字段序错误: order_id=%d order_no=%d created_at=%d", oi, on, ca)
	}
	if !autoInc["order_tab"]["order_id"] {
		t.Error("order_id（autoIncrement tag）应识别为自增列")
	}
}

// TestMergeTableColumnsOrder：自增第一 + 结构体序——渲染行顺序。
func TestMergeTableColumnsOrder(t *testing.T) {
	dir := ormStructFixture(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	ormStructs, _ := acts.ORMStructs(dir)
	rows := mergeTableColumnsWithSchema("order_tab", nil, nil, nil, ormStructs)
	var names []string
	for _, r := range rows {
		names = append(names, r.name)
	}
	// 自增 order_id 第一，然后 order_no/created_at 结构体序
	if len(names) != 3 || names[0] != "order_id" || names[1] != "order_no" || names[2] != "created_at" {
		t.Errorf("字段顺序 = %v, want [order_id order_no created_at]", names)
	}
}
