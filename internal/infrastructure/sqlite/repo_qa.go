package sqlite

// W2：历史问答存储（qa_history 表）——SaveQA 写入（ask/serve 对话
// 回答成功后），QAForSymbols 按符号/表名相关性查询（wiki --with-qa
// 参考资料）。

import (
	"github.com/schaepher/codeintel/internal/domain"
)

// SaveQA 写入一条历史问答。
func (r *Repo) SaveQA(qa *domain.QARecord) error {
	_, err := r.Exec(`INSERT INTO qa_history (question, answer, context, agent, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		qa.Question, qa.Answer, qa.Context, qa.Agent, qa.CreatedAt)
	return err
}

// QAForSymbols 按符号/表名相关性查询历史问答：context 或 question
// 包含任一关键字（LIKE 匹配），按时间倒序取前 limit 条。
// 相关性 = 该问答打包上下文/问题中出现过当前缺口相关的符号/表名。
func (r *Repo) QAForSymbols(keywords []string, limit int) ([]*domain.QARecord, error) {
	if len(keywords) == 0 || limit <= 0 {
		return nil, nil
	}
	args := make([]any, 0, len(keywords)*2)
	cond := ""
	for i, k := range keywords {
		if i > 0 {
			cond += " OR "
		}
		cond += "(context LIKE ? OR question LIKE ?)"
		args = append(args, "%"+k+"%", "%"+k+"%")
	}
	args = append(args, limit)
	rows, err := r.Query(`SELECT id, question, answer, context, agent, created_at
		FROM qa_history WHERE `+cond+` ORDER BY created_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.QARecord
	for rows.Next() {
		var qa domain.QARecord
		if err := rows.Scan(&qa.ID, &qa.Question, &qa.Answer, &qa.Context, &qa.Agent, &qa.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &qa)
	}
	return out, rows.Err()
}
