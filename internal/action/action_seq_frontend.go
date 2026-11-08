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
	// RequestBody B09d：请求体各字段相对当前动作形参的来源。
	RequestBody map[string]SeqFrontendBodyValue `json:"request_body,omitempty"`
	// Unknown 无法唯一连接的原因（缺 handler 源码/动态 URL/多候选）。
	Unknown string `json:"unknown,omitempty"`
}

// SeqFrontendBodyValue 请求体字段来源（parameter/literal/unknown）。
type SeqFrontendBodyValue struct {
	Kind      string   `json:"kind"`
	Parameter *int     `json:"parameter,omitempty"`
	Literal   string   `json:"literal,omitempty"`
	Path      []string `json:"path,omitempty"`
}

// seqFrontendBodyOrigin 解析期请求体来源（相对当前函数形参下标）。
type seqFrontendBodyOrigin struct {
	kind      string
	parameter int
	literal   string
	path      []string
}

// seqFrontendImport 导入声明（本地名 → 导出名 + 路径）。
type seqFrontendImport struct {
	path     string
	exported string
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
	body     []seqFrontendBodyField
	// B09e.4：形参局部名 → 位置 + 解构路径；函数体内局部声明/改写。
	paramBindings map[string]seqFrontendParamBinding
	shadowed      map[string]bool
	mutated       []string
}

// seqFrontendBodyField 请求体字段（name → 表达式）。
type seqFrontendBodyField struct {
	name string
	expr string
}

// seqFrontendCall 调用（包装函数传播实参位置）。
type seqFrontendCall struct {
	name string
	args []string
}

// seqFrontendBinding 事件绑定（文件 + 函数名）。
type seqFrontendBinding struct {
	file string
	name string
}

// seqAnalyzeFrontend 解析 frontendRoots 并连接已证明的后端 handler。
func seqAnalyzeFrontend(a *Actions, base CodeSequenceRequest,
	entries []SeqWorkflowEntry) *SeqWorkflowFrontend {
	if a == nil || len(base.FrontendRoots) == 0 {
		return nil
	}
	funcs := map[string]seqFrontendFunc{}                // file\x00name
	imports := map[string]map[string]seqFrontendImport{} // file → 本地名 → 导入
	var bindings []seqFrontendBinding
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
			seqParseFrontendFile(string(data), rel, funcs, imports, &bindings)
			return nil
		})
	}
	if len(bindings) == 0 {
		return &SeqWorkflowFrontend{Actions: []SeqFrontendAction{}}
	}
	routes := seqFrontendRouteMap(a)
	var actions []SeqFrontendAction
	seen := map[string]bool{}
	for _, b := range bindings {
		key := b.file + "\x00" + b.name
		if seen[key] {
			continue
		}
		seen[key] = true
		fn, ok := seqFrontendLookup(b.name, b.file, funcs, imports)
		if !ok || fn.file == "" || fn.name == "" {
			// B09e：事件已识别但声明/源码不足（缺文件、默认导出冲突、
			// 非函数导出）→ 保留动作 + 明确 unknown，不能静默丢掉事件。
			actions = append(actions, SeqFrontendAction{Action: b.name, File: b.file,
				Unknown: "declaration_not_found"})
			continue
		}
		path, method, body, reason := seqFrontendResolve(fn, funcs, imports,
			map[string]string{}, 0, map[string]bool{})
		act := SeqFrontendAction{Action: b.name, Method: method, Path: path,
			File: fn.file, Line: fn.line, Params: fn.params,
			RequestBody: seqFrontendBodyJSON(body)}
		if reason != "" || path == "" {
			if reason == "" {
				reason = "dynamic_url_or_missing_fetch"
			}
			act.Unknown = reason
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
	sort.Slice(actions, func(i, j int) bool {
		if actions[i].Action != actions[j].Action {
			return actions[i].Action < actions[j].Action
		}
		return actions[i].File < actions[j].File
	})
	return &SeqWorkflowFrontend{Actions: actions}
}

var (
	seqFrontendExportRe = regexp.MustCompile(`(?m)^\s*export\s+(?:async\s+)?function\s+(\w+)\s*\(([^)]*)\)`)
	// B09e：`export default function send(...)`——默认导出声明。
	seqFrontendDefaultExportRe = regexp.MustCompile(`(?m)^\s*export\s+default\s+(?:async\s+)?function\s+(\w+)\s*\(([^)]*)\)`)
	// B09e：`import send from './actions'` / `import send, { click } from './a'`。
	seqFrontendDefaultImportRe = regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z_$][\w$]*)\s*(?:,\s*\{([^}]*)\})?\s*from\s*['"]([^'"]+)['"]`)
	seqFrontendFetchArgRe      = regexp.MustCompile(`fetch\s*\(\s*([^,)]+)`)
	seqFrontendMethodRe        = regexp.MustCompile(`method\s*:\s*['"]([A-Za-z]+)['"]`)
	seqFrontendCallRe          = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(([^()]*)\)`)
	seqFrontendBindRe          = regexp.MustCompile(`on[A-Za-z]+\s*=\s*\{\s*\(\s*\)\s*=>\s*([A-Za-z_]\w*)\s*\(`)
	seqFrontendImportRe        = regexp.MustCompile(`(?m)import\s*\{([^}]*)\}\s*from\s*['"]([^'"]+)['"]`)
	seqFrontendReexportRe      = regexp.MustCompile(`(?m)export\s*\{([^}]*)\}\s*from\s*['"]([^'"]+)['"]`)
	seqFrontendBodyRe          = regexp.MustCompile(`body\s*:\s*(?:JSON\.stringify\s*\(\s*)?\{([^}]*)\}`)
)

// seqParseFrontendFile 解析一个前端文件的导出函数、导入与事件绑定。
func seqParseFrontendFile(src, rel string, funcs map[string]seqFrontendFunc,
	imports map[string]map[string]seqFrontendImport, bindings *[]seqFrontendBinding) {
	// B09e：默认导出声明（`export default function send`）与命名导出同样按
	// 文件的真实声明登记；同一文件多个默认导出 = 冲突（不按扫描顺序选）。
	defaultLocs := seqFrontendDefaultExportRe.FindAllStringSubmatchIndex(src, -1)
	for _, loc := range defaultLocs {
		name := src[loc[2]:loc[3]]
		fn := seqFrontendFuncFromSrc(src, rel, name, src[loc[4]:loc[5]], loc[0])
		funcs[rel+"\x00"+name] = fn
		if _, exists := funcs[rel+"\x00default"]; exists {
			// 冲突：不挑第一个/最后一个，明确无法解析默认导出。
			funcs[rel+"\x00default"] = seqFrontendFunc{file: rel}
			continue
		}
		funcs[rel+"\x00default"] = fn
	}
	locs := seqFrontendExportRe.FindAllStringSubmatchIndex(src, -1)
	for _, loc := range locs {
		name := src[loc[2]:loc[3]]
		fn := seqFrontendFuncFromSrc(src, rel, name, src[loc[4]:loc[5]], loc[0])
		funcs[rel+"\x00"+name] = fn
	}
	// B09e：默认导入 `import send from './actions'`（导出名为 default）。
	for _, m := range seqFrontendDefaultImportRe.FindAllStringSubmatch(src, -1) {
		if imports[rel] == nil {
			imports[rel] = map[string]seqFrontendImport{}
		}
		local := strings.TrimSpace(m[1])
		if local != "" {
			imports[rel][local] = seqFrontendImport{path: strings.TrimSpace(m[3]), exported: "default"}
		}
	}
	// 导入/重导出：`import {click as submitClick} from './a'`（B09c/B09d）。
	for _, m := range seqFrontendImportRe.FindAllStringSubmatch(src, -1) {
		if imports[rel] == nil {
			imports[rel] = map[string]seqFrontendImport{}
		}
		path := strings.TrimSpace(m[2])
		for _, spec := range strings.Split(m[1], ",") {
			local, exported := seqFrontendImportSpec(spec)
			if local == "" {
				continue
			}
			imports[rel][local] = seqFrontendImport{path: path, exported: exported}
		}
	}
	for _, m := range seqFrontendReexportRe.FindAllStringSubmatch(src, -1) {
		if imports[rel] == nil {
			imports[rel] = map[string]seqFrontendImport{}
		}
		path := strings.TrimSpace(m[2])
		for _, spec := range strings.Split(m[1], ",") {
			local, exported := seqFrontendImportSpec(spec)
			if local == "" {
				continue
			}
			imports[rel][local] = seqFrontendImport{path: path, exported: exported}
		}
	}
	for _, loc := range seqFrontendBindRe.FindAllStringSubmatchIndex(src, -1) {
		name := src[loc[2]:loc[3]]
		*bindings = append(*bindings, seqFrontendBinding{file: rel, name: name})
	}
}
