package domain

// W2：历史问答（ask 命令/serve 对话界面的 Q&A 收集）——wiki --with-qa
// 创建时作为 AI 参考资料，提升 wiki 语义层品质。

// QARecord 一条历史问答。
type QARecord struct {
	ID        int64  `json:"id"`
	Question  string `json:"question"`
	Answer    string `json:"answer"`
	Context   string `json:"context,omitempty"` // 打包的项目上下文摘要（符号/表名）
	Agent     string `json:"agent,omitempty"`
	CreatedAt int64  `json:"created_at"`
}
