package action

// todo25 §4.3：标准 HTTP 的 origin / 注册 / 准确续接。
//
// 证明链（首版边界）：
//   - 客户端调用必须是 `net/http.Get/Head/Post`（支持导入别名），URL 为
//     可静态解析的字符串字面量、scheme=http（https/相对/动态 → endpoint_unknown）。
//   - 服务端路由来自构建期 http_route 节点（path/method/handler_id/register
//     均为索引事实）；origin 由**注册点所在函数的直接语句** `HandleFunc` +
//     `ListenAndServe(<字面量 addr>, nil)` 证明，addr 必须与 URL host:port
//     完全相等；HTTP 方法按 route.method 兼容（空 = 任意）。
//   - 满足证明的注册 >1 → http_target_ambiguous；候选存在但无法证明（端口
//     不符/workspace 外/读取不完整）→ http_target_unproven。均不输出确定
//     服务端子树。
//   - depth=1 先保留准确 handler ID 再按 depth 停止，并登记跨仓续查前沿。

import (
	"go/ast"
	"go/token"
	"net/url"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// HTTP 续接证据/诊断（§4.3 专用）。
const (
	SeqEvidenceHTTPServer      = "http_server"
	SeqStopHTTPEndpointUnknown = "http_endpoint_unknown"
	SeqStopHTTPTargetUnproven  = "http_target_unproven"
	SeqStopHTTPTargetAmbiguous = "http_target_ambiguous"
	SeqReasonHTTPOrigin        = "http-origin+method+registered-handler"
)

// seqHTTPTarget 一个已证明的 HTTP 服务端 handler。
type seqHTTPTarget struct {
	repo    string
	acts    *Actions
	route   *domain.CodeEntity
	handler string
	file    string
	line    int
	addr    string
}

// seqHTTPEndpoint 解析出的客户端请求目标。
type seqHTTPEndpoint struct {
	method string
	host   string
	path   string
	raw    string
}

// seqExpandHTTPAt 调用点若是标准 HTTP 客户端调用则续接服务端（已处理返回
// true，无论成功/明确未命中——不再按外部库分类）。
func seqExpandHTTPAt(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	call *ast.CallExpr, f *ast.File, depth int, curFile string, pos token.Position) bool {
	method, ok := seqHTTPClientMethod(call.Fun, f)
	if !ok {
		return false
	}
	base := &SeqEvidence{Source: SeqEvidenceHTTPServer, CallFile: curFile, CallLine: pos.Line,
		Reason: SeqReasonHTTPOrigin}
	node.Evidence = base
	if len(call.Args) == 0 {
		node.Stop = &SeqStop{Code: SeqStopHTTPEndpointUnknown, Detail: "HTTP 调用没有 URL 实参"}
		return true
	}
	args := a.resolveSceneArgLiterals(req.curCtx.fset, req.curCtx.src, call, req.curBody,
		req.curParams, req.incomingArgs, req.incomingSrc, req.sceneConsts, maxSceneFlowDepth)
	raw := ""
	if len(args) > 0 {
		raw = args[0].val
	}
	ep, err := parseSeqHTTPEndpoint(raw, method)
	if err != nil {
		base.Verdict = SeqVerdictUnverified
		node.Stop = &SeqStop{Code: SeqStopHTTPEndpointUnknown, Detail: err.Error()}
		return true
	}
	targets, why, incomplete := seqHTTPProvenTargets(a, req, ep, curFile, pos.Line)
	switch {
	case len(targets) > 1:
		base.Ambiguous = true
		base.Verdict = SeqVerdictUnverified
		for _, t := range targets {
			base.Candidates = append(base.Candidates, t.handler)
		}
		node.Stop = &SeqStop{Code: SeqStopHTTPTargetAmbiguous,
			Detail: "同一 origin/path 有两个满足证明的注册（" + strings.Join(seqHTTPTargetIDs(targets), ", ") + "）"}
		return true
	case len(targets) == 0:
		base.Verdict = SeqVerdictUnverified
		node.Stop = &SeqStop{Code: SeqStopHTTPTargetUnproven, Detail: why}
		return true
	}
	t := targets[0]
	// T11：传输调用与服务端 handler 分开——调用点节点保留 net/http 传输身份
	// （http.Post），服务端 handler 作为其子节点（"HTTP POST <url>"），
	// handler 内部调用从该子节点发出（不出现空发送方）。
	transportID := "symbol:go:net/http:" + seqHTTPTransportFunc(ep.method)
	if transportID != "symbol:go:net/http:" {
		node.Evidence = &SeqEvidence{Source: SeqEvidenceAST, CallFile: curFile,
			CallLine: pos.Line, SymbolID: transportID, Confidence: 1.0,
			Reason: "net-http-transport"}
		// 传输参与者标为 net/http（库身份；不是 handler 的实现类型）。
		node.Type = "net/http"
	}
	handlerEv := &SeqEvidence{Source: SeqEvidenceHTTPServer, CallFile: curFile, CallLine: pos.Line,
		SymbolID: t.handler, File: t.file, Line: t.line, Confidence: 1.0,
		Verdict: SeqVerdictCorroborated, Reason: SeqReasonHTTPOrigin}
	if t.repo != req.RepoAbs {
		handlerEv.RemoteRepo = t.repo
	}
	if incomplete {
		handlerEv.Verdict = SeqVerdictCorroborated
	}
	child := &CodeSeqNode{Kind: "call", Actor: seqHTTPHandlerActor(t.handler),
		Label: "HTTP " + ep.method + " " + ep.raw, Line: t.line,
		Type: implTypeShort(t.handler), Evidence: handlerEv}
	seqExpandHTTPTarget(a, req, child, t, depth, curFile, pos.Line)
	node.Nodes = append(node.Nodes, child)
	return true
}

// seqHTTPClientMethod net/http 包级客户端调用 → HTTP 方法（支持导入别名）。
func seqHTTPClientMethod(fun ast.Expr, f *ast.File) (string, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Obj != nil {
		return "", false
	}
	if importAliases(f)[id.Name] != "net/http" {
		return "", false
	}
	switch sel.Sel.Name {
	case "Get":
		return "GET", true
	case "Head":
		return "HEAD", true
	case "Post":
		return "POST", true
	}
	return "", false
}

// parseSeqHTTPEndpoint 静态 URL → 目标（http/绝对/带 host；否则 endpoint unknown）。
func parseSeqHTTPEndpoint(raw, method string) (seqHTTPEndpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return seqHTTPEndpoint{}, errHTTP("URL 不是可静态解析的字符串字面量")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return seqHTTPEndpoint{}, errHTTP("URL 不是绝对地址（动态/相对地址无法证明 origin）")
	}
	if u.Scheme != "http" {
		return seqHTTPEndpoint{}, errHTTP("仅支持 scheme=http（" + u.Scheme + " 无法证明本地服务端 origin）")
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	return seqHTTPEndpoint{method: method, host: u.Host, path: p, raw: raw}, nil
}

type seqHTTPError string

func (e seqHTTPError) Error() string { return string(e) }

func errHTTP(s string) error { return seqHTTPError(s) }

// seqHTTPProvenTargets 在所有候选仓库里找满足 origin 证明的注册。
func seqHTTPProvenTargets(a *Actions, req CodeSequenceRequest,
	ep seqHTTPEndpoint, curFile string, curLine int) (targets []seqHTTPTarget, why string, incomplete bool) {
	repos := seqHTTPCandidateRepos(req)
	sawCandidate, sawMismatch, sawIncomplete := false, false, false
	for _, repoPath := range repos {
		acts, ok := seqHTTPRepoActions(a, req, repoPath)
		if !ok {
			sawIncomplete = true
			continue
		}
		routes, err := seqHTTPRoutes(acts, req, repoPath)
		if err != nil {
			sawIncomplete = true
			continue
		}
		for _, rt := range routes {
			if string(rt.ID) == "" || isOutboundRoute(string(rt.ID)) {
				continue
			}
			if rt.Property("path") != ep.path || !seqHTTPMethodCompatible(rt.Property("method"), ep.method) {
				continue
			}
			if rt.Property("handler_id") == "" {
				sawCandidate = true
				continue
			}
			sawCandidate = true
			t, ok, readOK := seqHTTPProveRoute(repoPath, acts, rt, ep.host, req.httpFiles)
			if !readOK {
				sawIncomplete = true
				continue
			}
			if !ok {
				sawMismatch = true
				continue
			}
			targets = append(targets, t)
		}
	}
	if sawIncomplete {
		return nil, "候选仓库/注册源码读取不完整（不能把'没读到'当'没有注册'）", true
	}
	switch {
	case len(targets) > 0:
		return targets, "", false
	case sawMismatch:
		return nil, "存在同 path 注册，但 origin 无法证明（端口/监听地址不符）", false
	case sawCandidate:
		return nil, "存在同 path 注册，但缺少可证明的 handler/canonical ID", false
	default:
		return nil, "候选范围内没有与该 origin/path/method 匹配的已注册路由（workspace 外或未注册）", false
	}
}

// seqHTTPCandidateRepos 起始仓库 + 显式候选范围（去重排序，范围外不参与）。
func seqHTTPCandidateRepos(req CodeSequenceRequest) []string {
	out := []string{req.RepoAbs}
	seen := map[string]bool{req.RepoAbs: true}
	for _, r := range req.CandidateRepos {
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// seqHTTPRepoActions 当前仓库复用 a；候选仓库按请求级资源打开（外层关闭）。
func seqHTTPRepoActions(a *Actions, req CodeSequenceRequest, repoPath string) (*Actions, bool) {
	if repoPath == req.RepoAbs || repoPath == "" {
		return a, true
	}
	if req.rpcRes == nil {
		return nil, false
	}
	return req.rpcRes.viewAs(req, repoPath, SeqStopHTTPTargetUnproven, "HTTP 候选仓库")
}

// seqHTTPRoutes 每个仓库的路由表请求内缓存（含失败缓存）。
func seqHTTPRoutes(acts *Actions, req CodeSequenceRequest, repoPath string) ([]*domain.CodeEntity, error) {
	if req.httpRoutes == nil {
		return acts.repo.GetHTTPRouteNodes()
	}
	if v, ok := req.httpRoutes[repoPath]; ok {
		return v, nil
	}
	nodes, err := acts.repo.GetHTTPRouteNodes()
	req.httpRoutes[repoPath] = nodes
	return nodes, err
}

// seqHTTPMethodCompatible HandleFunc 注册（空 method）匹配所有方法；显式方法
// 必须相等。
func seqHTTPMethodCompatible(registered, want string) bool {
	return registered == "" || want == "" || registered == want
}

func seqHTTPTargetIDs(ts []seqHTTPTarget) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.route.Property("register")+"→"+t.handler)
	}
	return out
}
