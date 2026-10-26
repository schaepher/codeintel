package action

// todo12-14 / todo13-14：证据身份（可复现请求 + CLI/分析器版本 + 索引构建
// 身份 + 配置身份）。源码 SHA 不能区分同提交重建/分析器/配置变化——这些
// 身份必须来自实际构建/配置，比对时明确报告差异，不假称复现同次分析。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime/debug"
	"strings"
)

// SeqEvidenceRequest 证据的可复现请求快照（结构化 argv 为权威）。
type SeqEvidenceRequest struct {
	Repo     string            `json:"repo"`
	Symbol   string            `json:"symbol"` // canonical ID（规范化）
	Depth    int               `json:"depth"`
	Focus    []string          `json:"focus,omitempty"`
	Scene    map[string]string `json:"scene,omitempty"`
	Evidence bool              `json:"evidence,omitempty"`
	Format   string            `json:"format,omitempty"`
	Out      string            `json:"out,omitempty"`
	IndexSHA string            `json:"index_sha,omitempty"`
	// todo13-14：影响结果的配置/身份（来自实际配置与构建）
	StopPkgs   []string   `json:"stop_packages,omitempty"`
	Filter     *SeqFilter `json:"filter,omitempty"`
	CLI        string     `json:"cli_version,omitempty"`
	Analyzer   string     `json:"analyzer_version,omitempty"`
	BuildID    string     `json:"build_id,omitempty"`
	BuiltAt    string     `json:"built_at,omitempty"`
	ConfigHash string     `json:"config_hash,omitempty"`
	Argv       []string   `json:"argv,omitempty"`
}

// SeqEvidenceIdentity 一次分析的完整身份（meta.evidence）。
type SeqEvidenceIdentity struct {
	SeqEvidenceRequest
	Argv []string `json:"argv"` // 规范化语义 argv（含输出/证据选项）
}

// analyzerVersion 分析器版本（构建信息：主模块版本 + VCS revision + dirty）。
func analyzerVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "", ""
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	ver := bi.Main.Version
	if ver == "" || ver == "(devel)" {
		ver = "devel"
	}
	return ver + "@" + rev + dirty
}

// seqConfigHash 影响结果的配置身份（过滤/停止包/场景/焦点/深度）。
func seqConfigHash(req CodeSequenceRequest) string {
	cfg := struct {
		Depth int
		Stop  []string
		Focus []string
		Scene map[string]string
		Fil   SeqFilter
	}{Depth: req.Depth, Stop: append([]string{}, req.StopPackages...),
		Focus: append([]string{}, req.Focus...), Scene: req.Scene.Values, Fil: req.Filter}
	b, _ := json.Marshal(cfg)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// SetCLIVersion 注入 CLI 版本（cli 包持有版本号；action 不反向依赖）。
func (r *CodeSequenceRequest) SetCLIVersion(v string) { r.cliVersion = v }

// seqEvidenceSnapshot 规范化请求快照（入口符号/全部 scene/focus/输出选项/配置）。
func seqEvidenceSnapshot(req CodeSequenceRequest, symbol string) *SeqEvidenceRequest {
	snap := &SeqEvidenceRequest{
		Repo: req.RepoAbs, Symbol: symbol, Depth: req.Depth,
		Focus: append([]string{}, req.Focus...), Evidence: req.Evidence,
		Format: req.Format, Out: req.Out, IndexSHA: req.indexSHA,
		CLI: "unknown", Analyzer: analyzerVersion(), BuildID: req.buildID, BuiltAt: req.builtAt,
		ConfigHash: seqConfigHash(req),
	}
	if req.Filter.Pkgs != nil || req.Filter.Files != nil || req.Filter.Fns != nil || req.Filter.Regex != nil {
		f := req.Filter
		snap.Filter = &f
	}
	if len(req.Scene.Values) > 0 {
		snap.Scene = map[string]string{}
		for k, v := range req.Scene.Values {
			snap.Scene[k] = v
		}
	}
	if len(req.StopPackages) > 0 {
		snap.StopPkgs = append([]string{}, req.StopPackages...)
	}
	snap.Argv = seqEvidenceArgv(snap)
	return snap
}

// seqEvidenceArgv 规范化语义 argv（**结构化原始参数**，不做 shell 转义；
// todo14-14：数组元素必须能直接 execve——空格/括号/#1/引号保持原字符）。
func seqEvidenceArgv(snap *SeqEvidenceRequest) []string {
	args := []string{"codeintel", "query", "sequence", snap.Symbol,
		"--repo", snap.Repo, "--code", "--depth", itoaSeq(snap.Depth)}
	keys := make([]string, 0, len(snap.Scene))
	for k := range snap.Scene {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		args = append(args, "--scene", k+"="+snap.Scene[k])
	}
	for _, f := range snap.Focus {
		args = append(args, "--focus", f)
	}
	if snap.Evidence {
		args = append(args, "--evidence")
	}
	if snap.Format != "" {
		args = append(args, "--format", snap.Format)
	}
	if snap.Out != "" {
		args = append(args, "--out", snap.Out)
	}
	args = append(args, "--json")
	return args
}

// seqEvidenceDisplayCommand 展示命令：对**参数值**做 shell quoting（供人复制
// 到 bash/zsh）。与结构化 Argv 是两个出口：Argv 不转义、可直接 execve。
func seqEvidenceDisplayCommand(argv []string) string {
	out := make([]string, 0, len(argv))
	for i, a := range argv {
		// 命令词/flag/纯数字保持原样；其余参数值一律 shell 引用（可读性
		// 与既有展示一致——safe 值也加引号，避免复制时歧义）
		if i < 3 || strings.HasPrefix(a, "-") || isAllDigits(a) {
			out = append(out, a)
			continue
		}
		out = append(out, shellQuote(a))
	}
	return strings.Join(out, " ")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// seqEvidenceQuery 证据的可复现命令（规范化；结构化字段见 Request）。
func seqEvidenceQuery(req CodeSequenceRequest, symbolID string) (string, *SeqEvidenceRequest) {
	// 重放入口整棵分析（evidence.SymbolID 只是该箭头指向的节点身份）
	sym := req.entryID
	if sym == "" {
		sym = req.Target
	}
	snap := seqEvidenceSnapshot(req, sym)
	snap.CLI = req.cliVersion
	if snap.CLI == "" {
		snap.CLI = "unknown"
	}
	return seqEvidenceDisplayCommand(snap.Argv), snap
}

// seqEvidenceIdentity meta 级身份（整次分析一份，比对用）。
func seqEvidenceIdentity(req CodeSequenceRequest) *SeqEvidenceIdentity {
	sym := req.entryID
	if sym == "" {
		sym = req.Target
	}
	snap := seqEvidenceSnapshot(req, sym)
	snap.CLI = req.cliVersion
	if snap.CLI == "" {
		snap.CLI = "unknown"
	}
	return &SeqEvidenceIdentity{SeqEvidenceRequest: *snap, Argv: snap.Argv}
}

// shellQuote POSIX shell 单引号转义（空格/括号/# 等安全）。
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// finishSeqEvidence 补证据身份（可复现命令 + 支持程度）——在所有具体化
// 分支结束后统一调用，避免每个分支重复写。
func finishSeqEvidence(req CodeSequenceRequest, n *CodeSeqNode) *CodeSeqNode {
	if n == nil || n.Evidence == nil {
		return n
	}
	q, snap := seqEvidenceQuery(req, n.Evidence.SymbolID)
	n.Evidence.Query = q
	n.Evidence.Request = snap
	n.Evidence.Verdict = seqEvidenceVerdict(n.Evidence)
	return n
}

// CompareSeqEvidenceIdentity 比对当前分析与保存的证据身份；返回差异说明
// （空 = 身份一致）。priorJSON 可为完整 sequence JSON（meta.evidence）或
// 单独的 identity 对象（{...}）。

// configSummary 配置差异的可读摘要。

// ReadSeqEvidenceFile 读取保存的 evidence JSON（完整输出或 identity 对象）。

// seqEvidenceSortKeys 供测试/渲染使用的稳定键（避免 map 迭代顺序）。
