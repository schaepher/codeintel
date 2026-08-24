package sqlite

// W2 qa_history 存储测试：SaveQA 写入 + QAForSymbols 相关性查询。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestQAHistory：写入 + 按符号/表名匹配查询 + limit。
func TestQAHistory(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewRepo(db)
	now := int64(1000)
	for i, qa := range []*domain.QARecord{
		{Question: "orders 表结构？", Answer: "orders 是订单表", Context: "orders", Agent: "claude", CreatedAt: now + 1},
		{Question: "main 入口？", Answer: "main 在 cmd", Context: "main", Agent: "claude", CreatedAt: now + 2},
		{Question: "无关问题", Answer: "无关", Context: "", Agent: "claude", CreatedAt: now + 3},
	} {
		if err := r.SaveQA(qa); err != nil {
			t.Fatalf("SaveQA[%d]: %v", i, err)
		}
	}
	// 按 orders 匹配：context LIKE 命中第 1 条
	recs, err := r.QAForSymbols([]string{"orders"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Question != "orders 表结构？" {
		t.Errorf("orders 匹配 = %+v; want 1 条 orders 问题", recs)
	}
	// 多关键字 OR + limit=1（时间倒序 → main 条目）
	recs, err = r.QAForSymbols([]string{"orders", "main"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Context != "main" {
		t.Errorf("多关键字 limit=1 = %+v; want main 条目", recs)
	}
	// 空关键字 / limit<=0 → nil
	if recs, _ := r.QAForSymbols(nil, 5); recs != nil {
		t.Errorf("空关键字应返回 nil: %+v", recs)
	}
	if recs, _ := r.QAForSymbols([]string{"orders"}, 0); recs != nil {
		t.Errorf("limit<=0 应返回 nil: %+v", recs)
	}
}
