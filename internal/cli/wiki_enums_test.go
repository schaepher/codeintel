package cli

// R29 proto 枚举解析测试：.proto 源 enum 块提取（顶层/嵌套/注释变体/
// option 容忍）——grpc 枚举支持（待办 6）。测试先行。

import (
	"github.com/schaepher/codeintel/internal/action"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractProtoEnums：顶层 + 嵌套枚举、注释变体（// 上一行、/** */、
// 尾注释）、option/保留字段容忍、proto package 提取。
func TestExtractProtoEnums(t *testing.T) {
	dir := t.TempDir()
	proto := `syntax = "proto3";
package service.v1;

// 订单状态
enum EOrderStatus {
  // 未支付
  Unpaid = 0;
  /** 已支付 */
  Paid = 1;
  Reserved = 2 [deprecated = true]; // 保留
}

message Order {
  // 来源
  enum ESource {
    Web = 0;
    App = 1;
  }
  string no = 1;
}
`
	if err := os.WriteFile(filepath.Join(dir, "global.proto"), []byte(proto), 0o644); err != nil {
		t.Fatal(err)
	}
	out := action.Enums(dir, false)
	if len(out) != 5 {
		t.Fatalf("枚举数 = %d; want 5（EOrderStatus 3 + Order.ESource 2）:\n%+v", len(out), out)
	}
	byName := map[string]action.EnumEntry{}
	for _, e := range out {
		byName[e.Type+"."+e.Name] = e
	}
	// 顶层枚举：Type=枚举名、Value=值号、Pkg=proto package
	if e, ok := byName["EOrderStatus.Unpaid"]; !ok {
		t.Error("缺 EOrderStatus.Unpaid")
	} else {
		if e.Value != "0" || e.Type != "EOrderStatus" || e.Comment != "未支付" {
			t.Errorf("Unpaid = %+v; want Value=0 Type=EOrderStatus Comment=未支付", e)
		}
		if e.Pkg != "service.v1" {
			t.Errorf("Pkg = %q; want proto package service.v1", e.Pkg)
		}
	}
	// /** */ 注释与尾注释
	if e, ok := byName["EOrderStatus.Paid"]; !ok || e.Comment != "已支付" {
		t.Errorf("Paid 注释 = %+v; want 已支付（/** */ 上一行）", byName["EOrderStatus.Paid"])
	}
	if e, ok := byName["EOrderStatus.Reserved"]; !ok || e.Comment != "保留" {
		t.Errorf("Reserved 注释 = %+v; want 保留（尾注释优先）", byName["EOrderStatus.Reserved"])
	}
	// 嵌套枚举：Type 带外层 message 前缀
	if e, ok := byName["Order.ESource.Web"]; !ok || e.Type != "Order.ESource" || e.Value != "0" {
		t.Errorf("嵌套枚举 = %+v; want Type=Order.ESource", byName["Order.ESource.Web"])
	}
	// Source 字段标注 proto
	if len(out) > 0 && out[0].Source != "proto" {
		t.Errorf("Source = %q; want proto", out[0].Source)
	}
}

// TestExtractEnumsMergesProto：extractEnums 并入 proto 枚举（go + proto
// 混合仓库）——同结构输出，source 区分；.pb.go 生成代码不重复扫。
func TestExtractEnumsMergesProto(t *testing.T) {
	dir := t.TempDir()
	// Go 枚举（internal/ 之外也要扫——R29 全仓扫）
	if err := os.MkdirAll(filepath.Join(dir, "pkg/order"), 0o755); err != nil {
		t.Fatal(err)
	}
	goSrc := `package order

type EStatus string

const (
	EStatusPending EStatus = "pending"
	EStatusPaid    EStatus = "paid"
)
`
	os.WriteFile(filepath.Join(dir, "pkg/order/status.go"), []byte(goSrc), 0o644)
	// proto 枚举
	os.WriteFile(filepath.Join(dir, "pkg/order/order.proto"), []byte(`syntax = "proto3";
package order.v1;
enum EState {
  S0 = 0;
  S1 = 1;
}
`), 0o644)
	// 生成代码 .pb.go（应被跳过——枚举由 proto 源提供）
	os.WriteFile(filepath.Join(dir, "pkg/order/order.pb.go"), []byte(`package order

type EState string

const (
	EState_S0 EState = "S0"
	EState_S1 EState = "S1"
)
`), 0o644)

	out := action.Enums(dir, true)
	var goN, protoN, pbN int
	for _, e := range out {
		switch e.Source {
		case "go":
			goN++
		case "proto":
			protoN++
		}
		if strings.Contains(e.File, "_pb.go") {
			pbN++
		}
	}
	if goN != 2 {
		t.Errorf("go 枚举数 = %d; want 2（EStatus 两个值）:\n%+v", goN, out)
	}
	if protoN != 2 {
		t.Errorf("proto 枚举数 = %d; want 2（EState 两个值）:\n%+v", protoN, out)
	}
	if pbN != 0 {
		t.Errorf("_pb.go 生成代码不应进枚举（%d 条）:\n%+v", pbN, out)
	}
}
