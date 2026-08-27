package cli

import (
	"encoding/json"
	"os"
)

// encodeJSON 输出结构化 JSON（stdout 唯一内容；query 命令共用）。
func encodeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// queryFlags 是 query 子命令的手动解析结果。
type queryFlags struct {
	repoPath         string
	includeUntyped   bool // R6：enums 是否包含无类型常量（默认只返回有类型枚举）
	depth            int
	maxDepth         int
	funcPath         string
	positional       []string
	json             bool
	compact          bool
	full             bool     // table-path --full（Q244：候选不截断）
	format           string   // summary 的 mermaid 输出（Q100）
	since            string   // unused 的 --since <ref>（git diff 区间）
	failOn           string   // unused 的 --fail-on unused|isolated（CI 退出码）
	all              bool     // relations --all：全库关联聚合（Q160）
	minConf          float64  // value-trace --min-conf：候选边置信度剪枝（Q161）
	minConfSet       bool     // --min-conf 显式设置（Q163 默认 1.0）
	includeContainer bool     // value-trace --include-container：父容器扩展（Q163）
	followIndirect   bool     // trace-backward --follow-indirect：跨函数间接写链（Q172）
	relTypes         []string // relations --type：关联类型过滤（query/write/read，可多次/逗号分隔；空=默认 query+write，P0④）
	maxHops          int      // relations --max-hops：跳数上限（0=不限）
	maxResults       int      // relations --max-results：条数上限（0=不限）
	includeLongQuery bool     // relations --include-long-query：query 不限制跳数（等价 --query-max-hops 0）
	queryMaxHops     int      // relations --query-max-hops：键关联跳数上限（0=不限制，默认 4）
	writeMaxHops     int      // relations --write-max-hops：同源写跳数上限（0=不限制，默认 4）
	readMaxHops      int      // relations --read-max-hops：间接读跳数上限（0=不限制，默认 4）
	memory           string   // relations --memory：full/sql（默认 auto 按规模，P0④）
	yamlPath         string   // R77：architecture/er/processes 的 wiki.yaml 路径（--yaml；缺省仓库根 wiki.yaml）
	maxEntries       int      // R77：processes 每节/每页入口展开上限（0 = 默认 15，同 wiki --max-entries）
	code             bool     // R81：sequence --code——代码级时序（AST 解析函数体：调用名 + 分支/循环）
	minPkgs          int      // R88：helpers --min-packages——跨包使用数阈值（0 = 配置默认 3）
	out              string   // R100：sequence --out——输出写文件（plantuml → PNG；否则文本）
}

// dispatchJSON 候选派发标注（Q157 P1：value-trace --json 输出）。
// edgeCandidateJSON 边级候选标注（Q161：动态 argument/returns 边元数据）。
type edgeCandidateJSON struct {
	Iface      string  `json:"interface"`
	Origin     string  `json:"origin"`
	Confidence float64 `json:"confidence"`
}

type dispatchJSON struct {
	Origin     string  `json:"origin"`
	Confidence float64 `json:"confidence"`
}
