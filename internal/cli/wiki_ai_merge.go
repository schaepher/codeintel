package cli

// wiki --ai 合并器：基于 yaml.Node 编辑 wiki.yaml——保留原文件注释与
// 未知字段（整树 marshal 会丢注释）；AI 填入的值标注 # AI 初稿
// （git diff 可回滚）。文件不存在时从空文档开始。

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// aiDraftComment AI 初稿来源标注。
const aiDraftComment = "# AI 初稿"

// yamlEditor wiki.yaml 节点树编辑器。
type yamlEditor struct {
	root *yaml.Node // DocumentNode
}

// loadYAMLEditor 读入节点树（保留注释）；文件不存在/为空 → 空文档。
func loadYAMLEditor(path string) (*yamlEditor, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return &yamlEditor{root: &yaml.Node{Kind: yaml.DocumentNode}}, nil
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return &yamlEditor{root: &yaml.Node{Kind: yaml.DocumentNode}}, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("解析 %s: %v", path, err)
	}
	return &yamlEditor{root: &root}, nil
}

// mapping 根 mapping 节点（无则创建）。
func (e *yamlEditor) mapping() *yaml.Node {
	if len(e.root.Content) == 0 {
		e.root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	return e.root.Content[0]
}

// keyValue mapping 中 key 的 value 节点（无则 nil）。
func keyValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// ensureKey 返回 mapping 中 key 的 value 节点（不存在则追加空标量，
// key 带 # AI 初稿 注释）。
func ensureKey(m *yaml.Node, key string) *yaml.Node {
	if v := keyValue(m, key); v != nil {
		return v
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, HeadComment: aiDraftComment}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ""}
	m.Content = append(m.Content, k, v)
	return v
}

// ensureSeq 返回根 mapping 下 key 的序列节点（不存在则追加）。
func (e *yamlEditor) ensureSeq(key string) *yaml.Node {
	m := e.mapping()
	if v := keyValue(m, key); v != nil && v.Kind == yaml.SequenceNode {
		return v
	}
	s := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, s)
	return s
}

// findItem 序列中 name 键值 == name 的项（mapping 节点；无则 nil）。
func findItem(seq *yaml.Node, name string) *yaml.Node {
	for _, c := range seq.Content {
		if c.Kind == yaml.MappingNode {
			if v := keyValue(c, "name"); v != nil && v.Value == name {
				return c
			}
		}
	}
	return nil
}

// appendItem 序列追加 mapping 项（name + 其余键值对），带 AI 初稿注释。
func appendItem(seq *yaml.Node, pairs ...string) *yaml.Node {
	it := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: aiDraftComment}
	for i := 0; i+1 < len(pairs); i += 2 {
		it.Content = append(it.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pairs[i]},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pairs[i+1]})
	}
	seq.Content = append(seq.Content, it)
	return it
}

// setScalar 标量赋值。
func setScalar(n *yaml.Node, val string) {
	n.Kind = yaml.ScalarNode
	n.Tag = "!!str"
	n.Value = val
}

// setModuleDesc modules 序列项（name 匹配或追加）→ description 赋值。
func (e *yamlEditor) setModuleDesc(name, desc string) {
	seq := e.ensureSeq("modules")
	it := findItem(seq, name)
	if it == nil {
		it = appendItem(seq, "name", name)
	}
	setScalar(ensureKey(it, "description"), desc)
}

// setTableAlias tables 序列项（name 匹配或追加）→ alias 赋值。
func (e *yamlEditor) setTableAlias(name, alias string) {
	seq := e.ensureSeq("tables")
	it := findItem(seq, name)
	if it == nil {
		it = appendItem(seq, "name", name)
	}
	setScalar(ensureKey(it, "alias"), alias)
}

// setColumnComments tables[name].columns 序列项 → comment 赋值
// （列不存在则追加）。
func (e *yamlEditor) setColumnComments(tbl string, comments map[string]string) {
	seq := e.ensureSeq("tables")
	it := findItem(seq, tbl)
	if it == nil {
		it = appendItem(seq, "name", tbl)
	}
	colSeq := keyValue(it, "columns")
	if colSeq == nil || colSeq.Kind != yaml.SequenceNode {
		colSeq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		it.Content = append(it.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "columns"}, colSeq)
	}
	for col, comment := range comments {
		ci := findItem(colSeq, col)
		if ci == nil {
			ci = appendItem(colSeq, "name", col)
		}
		setScalar(ensureKey(ci, "comment"), comment)
	}
}

// setDomain domains 序列项（name 匹配或追加）→ description/packages/
// tables/services 赋值（R34 业务域——AI 初稿标注由 ensureKey/appendItem
// 负责；R38 services——服务归属领域）。
func (e *yamlEditor) setDomain(d wikiDomainCfg) {
	seq := e.ensureSeq("domains")
	it := findItem(seq, d.Name)
	if it == nil {
		it = appendItem(seq, "name", d.Name)
	}
	if d.Description != "" {
		setScalar(ensureKey(it, "description"), d.Description)
	}
	if len(d.Packages) > 0 {
		setStringSeq(ensureKey(it, "packages"), d.Packages)
	}
	if len(d.Tables) > 0 {
		setStringSeq(ensureKey(it, "tables"), d.Tables)
	}
	if len(d.Services) > 0 {
		setStringSeq(ensureKey(it, "services"), d.Services)
	}
	if len(d.Subdomains) > 0 {
		// R80：AI 划分子域（嵌套结构——name/description/packages/tables）
		subSeq := ensureKey(it, "subdomains")
		subSeq.Kind = yaml.SequenceNode
		subSeq.Tag = "!!seq"
		subSeq.Content = nil
		for _, sd := range d.Subdomains {
			item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			item.Content = append(item.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: sd.Name})
			if sd.Description != "" {
				item.Content = append(item.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "description"},
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: sd.Description})
			}
			if len(sd.Packages) > 0 {
				pkgSeq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
				for _, p := range sd.Packages {
					pkgSeq.Content = append(pkgSeq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p})
				}
				item.Content = append(item.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "packages"}, pkgSeq)
			}
			if len(sd.Tables) > 0 {
				tblSeq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
				for _, t := range sd.Tables {
					tblSeq.Content = append(tblSeq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t})
				}
				item.Content = append(item.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tables"}, tblSeq)
			}
			subSeq.Content = append(subSeq.Content, item)
		}
	}
}

// clearDomains 清空 domains 序列（R38：domains 分析是整体重归纳——
// 旧域名变更后残留会与新域并存（go2o 实测 16 域 = 旧 8 + 新 8））。
func (e *yamlEditor) clearDomains() {
	m := e.mapping()
	if n := keyValue(m, "domains"); n != nil && n.Kind == yaml.SequenceNode {
		n.Content = nil
	}
}

// setStringSeq 键值设为字符串序列（域归属的包/表清单）。
func setStringSeq(n *yaml.Node, vals []string) {
	n.Kind = yaml.SequenceNode
	n.Tag = "!!seq"
	n.Content = nil
	for _, v := range vals {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
}

// setGlossary glossary 序列项（term 匹配或追加）→ english/abbr/
// definition 赋值（R84：英文术语 + 缩写字段）。
func (e *yamlEditor) setGlossary(term, english, abbr, def string) {
	seq := e.ensureSeq("glossary")
	it := findItem(seq, term)
	if it == nil {
		it = appendItem(seq, "term", term)
	}
	if english != "" {
		setScalar(ensureKey(it, "english"), english)
	}
	if abbr != "" {
		setScalar(ensureKey(it, "abbr"), abbr)
	}
	setScalar(ensureKey(it, "definition"), def)
}

// save 写回文件（缩进 2）。空文档（文件不存在/空文件加载）先初始化
// 根 mapping——yaml.v3 无法编码 Content 为空的 DocumentNode
// （报 "expected SCALAR, SEQUENCE-START, MAPPING-START, or ALIAS,
// but got document end"，曾致 --ai 首跑静默丢文件）。
func (e *yamlEditor) save(path string) error {
	if len(e.root.Content) == 0 {
		e.mapping()
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(e.root); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
