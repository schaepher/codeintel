package cli

// R45 外部接口调用 wiki 节（从 query_external_interfaces.go 拆出——
// 行数治理）：md/html 双通道展示外部系统接口调用（接口未在本项目
// 定义 + 请求对象不在本项目服务参数）。R94：数据改经
// Actions.ExternalInterfaces 同源调用（查询逻辑迁 action）。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// joinCallers 调用点摘要（前 3 个）。
func joinCallers(cs []action.ExtCaller) string {
	var parts []string
	for i, c := range cs {
		if i >= 3 {
			parts = append(parts, fmt.Sprintf("等 %d 处", len(cs)-3))
			break
		}
		parts = append(parts, c.Func+"("+c.Loc+")")
	}
	return strings.Join(parts, "、")
}

// renderExternalInterfacesMD 外部接口调用节（md——R45 wiki 消费；
// 无外部接口返回空）。
func renderExternalInterfacesMD(acts *action.Actions) string {
	res, err := acts.ExternalInterfaces()
	if err != nil || len(res.Interfaces) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 外部接口调用\n\n")
	b.WriteString("> 数据源：grpc_call/http_call 边 + 节点特征——调用接口未在本项目定义（grpc 服务无注册/方法、http 路由无 handler）且请求对象不在本项目服务参数中。\n\n")
	cur := ""
	for _, ei := range res.Interfaces {
		key := ei.Kind + "|" + ei.Service
		if key != cur {
			cur = key
			b.WriteString(fmt.Sprintf("### [%s] %s\n\n", ei.Kind, ei.Service))
		}
		b.WriteString(fmt.Sprintf("- `%s`", ei.Method))
		if ei.ReqType != "" {
			b.WriteString(fmt.Sprintf("（请求 `%s`）", ei.ReqType))
		}
		b.WriteString(" ← " + joinCallers(ei.Callers) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// renderExternalInterfacesHTML 外部接口调用节（html——R45）。
func renderExternalInterfacesHTML(acts *action.Actions) string {
	res, err := acts.ExternalInterfaces()
	if err != nil || len(res.Interfaces) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="external"><h2>外部接口调用</h2><p class="muted">数据源：grpc_call/http_call 边 + 节点特征——调用接口未在本项目定义且请求对象不在本项目服务参数中（外部系统集成点）。</p>`)
	cur := ""
	for _, ei := range res.Interfaces {
		key := ei.Kind + "|" + ei.Service
		if key != cur {
			cur = key
			b.WriteString(fmt.Sprintf(`<h3>[%s] %s</h3><ul>`, htmlEsc(ei.Kind), htmlEsc(ei.Service)))
		}
		b.WriteString("<li><code>" + htmlEsc(ei.Method) + "</code>")
		if ei.ReqType != "" {
			b.WriteString("（请求 <code>" + htmlEsc(ei.ReqType) + "</code>）")
		}
		b.WriteString(" ← " + htmlEsc(joinCallers(ei.Callers)) + "</li>")
	}
	b.WriteString("</ul></section>")
	return b.String()
}
