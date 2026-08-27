package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// cmdWikiInit 生成 wiki.yaml 骨架（存在则不覆盖）。
func cmdWikiInit(abs string) int {
	path := filepath.Join(abs, "wiki.yaml")
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("wiki.yaml 已存在: %s（未覆盖——如需重新生成请先删除）\n", path)
		return 0
	}
	tpl := `# codeintel wiki 配置——人工补充业务描述（AI 初稿见 wiki skill 两模式）
# 生成的 wiki 内容里标注来源（wiki.yaml / 包注释 / 代码分析），
# 描述越全，wiki 越接近人工维护的文档。
project:
  description: # 项目一句话描述（显示在概览页顶部）

modules:
  # - name: <module 全名，如 example.com/app>  # 白名单：列出则只生成这些模块
  #   description: 模块职责（一句话）           # 显示在模块页"职责"段
  #   order: 1                                  # 概览页模块顺序（小在前）

tables:
  # - name: <表名>
  #   alias: 表的中文别名
  #   columns:
  #     - name: <列名>
  #       type: TEXT
  #       default: ""
  #       comment: 列说明
  #   indexes:
  #     - UNIQUE INDEX idx_x (a, b)
  #   ddl: |-
  #     CREATE TABLE ...
  #   hidden: true        # 从 wiki 隐藏（噪音表）

# architecture: |-
#   graph LR
#     A[A] --> B[B]

# glossary:
#   - term: 术语
#     definition: 定义

# flows:
#   - title: 业务时序
#     mermaid: |-
#       sequenceDiagram
#         A->>B: ...
`
	if err := os.WriteFile(path, []byte(tpl), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: 写入 %s: %v\n", path, err)
		return 1
	}
	fmt.Printf("wiki.yaml 骨架已生成: %s（按注释提示补充后运行 codeintel wiki）\n", path)
	return 0
}
