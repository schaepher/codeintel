// asttool：go/ast 文件操作工具链（供大文件拆分/注释整理复用）。
//
// 子命令：
//
//	asttool analyze <file...>                      列出顶层声明清单
//	asttool funcsize <file...>                     函数/方法行数统计（降序）
//	asttool split <src.go> <out1.go:name1,name2> ... 按声明分组拆分文件
//	asttool orphan <file...>                       列出孤立顶层注释（拆分残留）
//	asttool rename <file.go> <old> <new>           符号重命名（go/ast 安全变换）
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "analyze":
		analyzeMain(os.Args[2:])
	case "split":
		splitMain(os.Args[2:])
	case "orphan":
		orphanMain(os.Args[2:])
	case "funcsize":
		funcsizeMain(os.Args[2:])
	case "rename":
		renameMain(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  asttool analyze <file...>
  asttool split <src.go> <out1.go:name1,name2> [<out2.go:...>...]
  asttool rename <file.go> <old> <new> [--scope pkg|file] [--dry-run] [--include-tests]
  asttool orphan <file...>
  asttool funcsize <file...>
see file headers for details`)
}
