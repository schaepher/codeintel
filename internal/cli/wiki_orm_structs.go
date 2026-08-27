package cli

// R20 表关联结构体：源码扫描 TableName() 方法反查结构体（ORM 结构体
// ↔ 表），提取结构体定义源码片段——表定义上方展示，可折叠展开供
// 用户核对字段映射。纯工具（源码事实），零 AI。
// R100：扫描逻辑迁 action（Actions.ORMStructs——返回 domain.ORMStruct）；
// 本文件只留渲染（组合 action 结果）。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// renderORMStructSectionMD 表上方关联结构体区块（md：<details> 折叠）。
func renderORMStructSectionMD(tbl string, structs []domain.ORMStruct) string {
	if len(structs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range structs {
		b.WriteString(fmt.Sprintf("<details><summary>关联结构体 <code>%s</code>（%s:%d）——展开核对字段映射</summary>\n\n",
			s.Name, s.File, s.Line))
		b.WriteString("```go\n" + s.Code + "\n```\n")
		b.WriteString("</details>\n\n")
	}
	return b.String()
}

// renderORMStructSectionHTML 表上方关联结构体区块（html：fold-btn
// 折叠，与模块区块同机制）。
func renderORMStructSectionHTML(tbl string, structs []domain.ORMStruct) string {
	if len(structs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range structs {
		b.WriteString(fmt.Sprintf(`<h4 class="fold-btn" data-target="orm-%s-%s" data-label="1">▸ 关联结构体 %s（%s:%d）——展开核对字段映射</h4><div class="sec-body" id="orm-%s-%s" style="display:none"><pre class="code">%s</pre></div>`,
			tbl, s.Name, htmlEsc(s.Name), htmlEsc(s.File), s.Line, tbl, s.Name, htmlEsc(s.Code)))
	}
	return b.String()
}
