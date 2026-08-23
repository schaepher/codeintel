package main

// funcsize 子命令：分析 Go 文件所有函数/方法的行数，按从大到小排序
// （行数治理辅助——先看哪些函数是大头，再决定拆文件还是拆函数）。
//
//	asttool funcsize <file.go...>
//
// 输出：行数降序 | 名称（方法为 (Receiver).Name）| 起止行。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
)

// funcSize 一个函数/方法的行数统计。
type funcSize struct {
	name      string
	lines     int
	startLine int
	endLine   int
}

// funcSizes 分析单文件的全部函数/方法行数（含 receiver；行数 = 结束
// 行 - 起始行 + 1）。
func funcSizes(path string) []funcSize {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil
	}
	var out []funcSize
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			// 方法：receiver 类型名（含指针）
			recv := receiverName(fd.Recv.List[0].Type)
			name = "(" + recv + ")." + name
		}
		start := fset.Position(fd.Pos()).Line
		end := fset.Position(fd.End()).Line
		out = append(out, funcSize{name: name, lines: end - start + 1, startLine: start, endLine: end})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].lines != out[j].lines {
			return out[i].lines > out[j].lines
		}
		return out[i].name < out[j].name
	})
	return out
}

// receiverName 方法 receiver 类型名（*T → T；含包限定符）。
func receiverName(t ast.Expr) string {
	switch v := t.(type) {
	case *ast.StarExpr:
		return receiverName(v.X)
	case *ast.Ident:
		return v.Name
	case *ast.IndexExpr: // 泛型 T[X]
		return receiverName(v.X)
	case *ast.SelectorExpr: // pkg.T
		return v.Sel.Name
	}
	return "?"
}

// funcsizeMain 子命令入口。
func funcsizeMain(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: asttool funcsize <file.go...>")
		os.Exit(2)
	}
	for _, path := range args {
		sizes := funcSizes(path)
		if sizes == nil {
			os.Exit(1)
		}
		fmt.Printf("== %s（%d 个函数/方法）\n", path, len(sizes))
		for _, s := range sizes {
			fmt.Printf("  %4d | %-40s | %d-%d\n", s.lines, s.name, s.startLine, s.endLine)
		}
	}
}
