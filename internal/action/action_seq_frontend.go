package action

// B09：前端源码动作 → 请求方法/路径 → 已证明后端 handler（只选择源码范围，
// 不配置业务动作顺序）。事件绑定必须来自真实 onClick 等绑定，不凭函数名猜。

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SeqFrontendAction 前端动作（源码位置 + 方法/路径 + 入口身份）。
type SeqFrontendAction struct {
	Action string   `json:"action"`
	Method string   `json:"method"`
	Path   string   `json:"path"`
	Entry  string   `json:"entry,omitempty"`
	File   string   `json:"file,omitempty"`
	Line   int      `json:"line,omitempty"`
	Params []string `json:"params,omitempty"`
	// Unknown 无法唯一连接的原因（缺 handler 源码/动态 URL/多候选）。
	Unknown string `json:"unknown,omitempty"`
}

// SeqWorkflowFrontend 前端动作集合。
type SeqWorkflowFrontend struct {
	Actions []SeqFrontendAction `json:"actions"`
}

// seqFrontendFunc 解析出的前端函数。
type seqFrontendFunc struct {
	name     string
	file     string
	line     int
	params   []string
	path     string
	fetchArg string
	method   string
	calls    []seqFrontendCall
}

// seqFrontendCall 调用（包装函数传播实参位置）。
type seqFrontendCall struct {
	name string
	args []string
}

// seqAnalyzeFrontend 解析 frontendRoots 并连接已证明的后端 handler。
func seqAnalyzeFrontend(a *Actions, base CodeSequenceRequest,
	entries []SeqWorkflowEntry) *SeqWorkflowFrontend {
	if a == nil || len(base.FrontendRoots) == 0 {
		return nil
	}
	funcs := map[string]seqFrontendFunc{}
	bindings := map[string]string{} // 事件绑定的函数名 → 动作名（同值）
	var bindOrder []string
	for _, root := range base.FrontendRoots {
		dir := root
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(base.RepoAbs, dir)
		}
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".ts" && ext != ".tsx" && ext != ".js" && ext != ".jsx" {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			rel, _ := filepath.Rel(base.RepoAbs, path)
			seqParseFrontendFile(string(data), rel, funcs, bindings, &bindOrder)
			return nil
		})
	}
	if len(bindings) == 0 {
		return &SeqWorkflowFrontend{Actions: []SeqFrontendAction{}}
	}
	routes := seqFrontendRouteMap(a)
	var actions []SeqFrontendAction
	seen := map[string]bool{}
	for _, name := range bindOrder {
		if seen[name] {
			continue
		}
		seen[name] = true
		fn, ok := funcs[name]
		if !ok {
			continue
		}
		path, method, ok := seqFrontendResolveFetch(fn, funcs, map[string]string{}, 0)
		act := SeqFrontendAction{Action: name, Method: method, Path: path,
			File: fn.file, Line: fn.line, Params: fn.params}
		if !ok || path == "" {
			act.Unknown = "dynamic_url_or_missing_fetch"
			actions = append(actions, act)
			continue
		}
		handlers := routes[strings.ToUpper(method)+" "+path]
		switch len(handlers) {
		case 1:
			act.Entry = handlers[0]
		case 0:
			act.Unknown = "handler_not_found"
		default:
			act.Unknown = "ambiguous_handler"
		}
		actions = append(actions, act)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Action < actions[j].Action })
	return &SeqWorkflowFrontend{Actions: actions}
}

var (
	seqFrontendExportRe   = regexp.MustCompile(`(?m)^\s*export\s+(?:async\s+)?function\s+(\w+)\s*\(([^)]*)\)`)
	seqFrontendFetchArgRe = regexp.MustCompile(`fetch\s*\(\s*([^,)]+)`)
	seqFrontendMethodRe   = regexp.MustCompile(`method\s*:\s*['"]([A-Za-z]+)['"]`)
	seqFrontendCallRe     = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(([^()]*)\)`)
	seqFrontendBindRe     = regexp.MustCompile(`on[A-Za-z]+\s*=\s*\{\s*\(\s*\)\s*=>\s*([A-Za-z_]\w*)\s*\(`)
)

// seqParseFrontendFile 解析一个前端文件的导出函数与事件绑定。
func seqParseFrontendFile(src, rel string, funcs map[string]seqFrontendFunc,
	bindings map[string]string, order *[]string) {
	locs := seqFrontendExportRe.FindAllStringSubmatchIndex(src, -1)
	for _, loc := range locs {
		name := src[loc[2]:loc[3]]
		params := seqFrontendParams(src[loc[4]:loc[5]])
		line := 1 + strings.Count(src[:loc[0]], "\n")
		body := seqFrontendBody(src, loc[1])
		fn := seqFrontendFunc{name: name, file: rel, line: line, params: params}
		if m := seqFrontendFetchArgRe.FindStringSubmatch(body); m != nil {
			arg := strings.TrimSpace(m[1])
			if lit, ok := seqFrontendStringLit(arg); ok {
				fn.path = lit
			} else {
				fn.fetchArg = arg
			}
		}
		if m := seqFrontendMethodRe.FindStringSubmatch(body); m != nil {
			fn.method = strings.ToUpper(m[1])
		} else if fn.path != "" {
			fn.method = "GET"
		}
		for _, m := range seqFrontendCallRe.FindAllStringSubmatch(body, -1) {
			name := m[1]
			if name == "fetch" || name == "function" || name == "if" || name == "return" ||
				name == "JSON" || name == "stringify" {
				continue
			}
			fn.calls = append(fn.calls, seqFrontendCall{name: name,
				args: seqFrontendSplitArgs(m[2])})
		}
		funcs[name] = fn
	}
	for _, loc := range seqFrontendBindRe.FindAllStringSubmatchIndex(src, -1) {
		name := src[loc[2]:loc[3]]
		if _, ok := bindings[name]; !ok {
			bindings[name] = name
			*order = append(*order, name)
		}
	}
}
