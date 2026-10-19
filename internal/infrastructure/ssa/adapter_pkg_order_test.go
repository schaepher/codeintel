package ssa

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestIndexPkgsByPath（todo8-8）：重复路径取原切片第一个；缺失为 nil；
// 空输入空表。
func TestIndexPkgsByPath(t *testing.T) {
	p1 := &packages.Package{PkgPath: "example.com/a"}
	p2 := &packages.Package{PkgPath: "example.com/a"} // 重复路径
	p3 := &packages.Package{PkgPath: "example.com/b"}
	idx := indexPkgsByPath([]*packages.Package{p1, nil, p2, p3})
	if idx["example.com/a"] != p1 {
		t.Error("重复路径应保留原切片第一个")
	}
	if idx["example.com/b"] != p3 {
		t.Error("b 路径缺失")
	}
	if _, ok := idx["example.com/missing"]; ok {
		t.Error("缺失路径不应命中")
	}
	if len(indexPkgsByPath(nil)) != 0 {
		t.Error("空输入应返回空表")
	}
}
