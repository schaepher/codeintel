package ssa

// todo13-10：未知回调调用结果节点带 call_unknown 标记（value-trace 停止原因）。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func TestUnknownCallbackMarkedOnCallResult(t *testing.T) {
	nodes, _ := indexFixture(t, map[string]string{
		"go.mod": "module example.com/mtest\n\ngo 1.21\n",
		"main.go": `package vt

type State string

type StateHandler struct{ State State }

func (h *StateHandler) Unsupported(f func() State) State {
	h.State = f()
	return h.State
}
`,
	})
	var marked *domain.CodeEntity
	for _, n := range nodes {
		if n.Kind == domain.KindSSAValue && n.Property("call_unknown") != "" {
			marked = n
		}
	}
	if marked == nil {
		t.Fatalf("未知回调的调用结果节点应带 call_unknown 标记: %+v", nodes)
	}
	if marked.Property("call_unknown") != "callback" {
		t.Errorf("reason 应为 callback: %+v", marked.Properties)
	}
	if reason := marked.Property("call_reason"); !strings.Contains(reason, "函数形参 f") {
		t.Errorf("应说明未知回调身份: %q", reason)
	}
	if fmt.Sprint(marked.Properties["call_line"]) != "8" || marked.Property("call_file") != "main.go" {
		t.Errorf("应带相对文件与正行号: %+v", marked.Properties)
	}
}
