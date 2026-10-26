package action

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// CompareSeqEvidenceIdentity 比对当前分析与保存的证据身份；返回差异说明
// （空 = 身份一致）。priorJSON 可为完整 sequence JSON（meta.evidence）或
// 单独的 identity 对象（{...}）。
func CompareSeqEvidenceIdentity(cur *SeqEvidenceIdentity, priorJSON []byte) ([]string, error) {
	if cur == nil {
		return []string{"当前分析没有证据身份（未启用 --evidence）"}, nil
	}
	var wrap struct {
		Meta *struct {
			Evidence *SeqEvidenceIdentity `json:"evidence"`
		} `json:"meta"`
		Evidence *SeqEvidenceIdentity `json:"evidence"`
	}
	if err := json.Unmarshal(priorJSON, &wrap); err != nil {
		return nil, err
	}
	prev := (*SeqEvidenceIdentity)(nil)
	switch {
	case wrap.Meta != nil && wrap.Meta.Evidence != nil:
		prev = wrap.Meta.Evidence
	case wrap.Evidence != nil:
		prev = wrap.Evidence
	default:
		var one SeqEvidenceIdentity
		if err := json.Unmarshal(priorJSON, &one); err != nil {
			return nil, err
		}
		prev = &one
	}
	if prev == nil || (prev.IndexSHA == "" && prev.BuildID == "" && prev.Analyzer == "") {
		return []string{"原证据身份为空或格式不识别（无法证明复现同一次分析）"}, nil
	}
	var diffs []string
	if prev.IndexSHA != cur.IndexSHA {
		diffs = append(diffs, "源码 SHA 变化："+orUnknown(prev.IndexSHA)+" → "+orUnknown(cur.IndexSHA))
	}
	if prev.BuildID != cur.BuildID || prev.BuiltAt != cur.BuiltAt {
		diffs = append(diffs, "索引构建身份变化："+orUnknown(prev.BuildID)+"@"+orUnknown(prev.BuiltAt)+
			" → "+orUnknown(cur.BuildID)+"@"+orUnknown(cur.BuiltAt))
	}
	if prev.Analyzer != cur.Analyzer {
		diffs = append(diffs, "分析器版本变化："+orUnknown(prev.Analyzer)+" → "+orUnknown(cur.Analyzer))
	}
	if prev.CLI != cur.CLI {
		diffs = append(diffs, "CLI 版本变化："+orUnknown(prev.CLI)+" → "+orUnknown(cur.CLI))
	}
	if prev.ConfigHash != cur.ConfigHash {
		diffs = append(diffs, "配置身份变化（filter/停止包/场景/焦点/深度）："+
			orUnknown(prev.ConfigHash)+" → "+orUnknown(cur.ConfigHash)+
			"；prev="+configSummary(&prev.SeqEvidenceRequest)+" now="+configSummary(&cur.SeqEvidenceRequest))
	}
	if !sameStrings(prev.Argv, cur.Argv) {
		diffs = append(diffs, "规范化 argv 变化："+strings.Join(prev.Argv, " ")+" → "+strings.Join(cur.Argv, " "))
	}
	if prev.Symbol != cur.Symbol || prev.Repo != cur.Repo {
		diffs = append(diffs, "请求目标变化："+prev.Repo+"/"+prev.Symbol+" → "+cur.Repo+"/"+cur.Symbol)
	}
	return diffs, nil
}

// configSummary 配置差异的可读摘要。
func configSummary(s *SeqEvidenceRequest) string {
	if s == nil {
		return "{}"
	}
	return "stop=" + strings.Join(s.StopPkgs, ",") +
		" filter=" + filterSummary(s.Filter)
}
func filterSummary(f *SeqFilter) string {
	if f == nil {
		return "{}"
	}
	return strings.Join([]string{
		"pkgs:" + strings.Join(f.Pkgs, ","),
		"files:" + strings.Join(f.Files, ","),
		"fns:" + strings.Join(f.Fns, ","),
		"regex:" + strings.Join(f.Regex, ","),
	}, ";")
}
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ReadSeqEvidenceFile 读取保存的 evidence JSON（完整输出或 identity 对象）。
func ReadSeqEvidenceFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// seqEvidenceSortKeys 供测试/渲染使用的稳定键（避免 map 迭代顺序）。
func seqEvidenceSortKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
