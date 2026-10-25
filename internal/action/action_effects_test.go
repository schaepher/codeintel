package action

// todo10-7：query effects 验收（partial_write 夹具）。
//   - 主写入成功后追踪失败的"部分成功"：写入可列出，错误返回可见
//   - 错误被后续赋值覆盖 → suspicious（可疑，verdict=unverified）
//   - 事务范围只陈述观察到的事实（不推断"全部写入回滚"）

import (
	"strings"
	"testing"
)

// TestEffectsPartialWrite：主写入 + 追踪记录 → 两组写入，返回错误可见。
func TestEffectsPartialWrite(t *testing.T) {
	dir, acts := indexTodo10Fixture(t, "partial_write")
	res, err := acts.Effects(EffectsRequest{
		Target: "symbol:go:example.com/fx/partial_write:Commit", RepoAbs: dir, Depth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Function == "" || res.File == "" {
		t.Fatalf("应定位到函数与源码文件: %+v", res)
	}
	if len(res.DBWrites) == 0 {
		t.Fatalf("应列出字段写入（summary 来源）: %+v", res.DBWrites)
	}
	// 每个写入都带证据（direct/corroborated/unverified + 原因）
	for _, w := range res.DBWrites {
		if w.Evidence == nil || w.Evidence.Verdict == "" {
			t.Errorf("写入条目缺证据: %+v", w)
		}
		if w.Table == "" && w.Evidence.Verdict != SeqVerdictUnverified {
			t.Errorf("无表列映射时应标 unverified: %+v", w)
		}
	}
	// 返回值/错误来自源码
	if res.Result.Line == 0 || res.Result.Verdict != SeqVerdictCorroborated {
		t.Errorf("应记录 return 与错误变量: %+v", res.Result)
	}
	if len(res.Result.Returns) == 0 {
		t.Errorf("应记录返回表达式: %+v", res.Result)
	}
	// 事务：未观察到 → none + unverified（不推断回滚）
	if res.Tx.Kind != "none" {
		t.Errorf("该夹具无事务调用，Tx.Kind = %q", res.Tx.Kind)
	}
	if res.Tx.Verdict != SeqVerdictUnverified {
		t.Errorf("未观察到事务时应 unverified（不推断回滚）: %+v", res.Tx)
	}
}

// TestEffectsErrorOverwrite：错误被覆盖 + 返回 nil → suspicious（可疑形态）。
func TestEffectsErrorOverwrite(t *testing.T) {
	dir, acts := indexTodo10Fixture(t, "partial_write")
	res, err := acts.Effects(EffectsRequest{
		Target: "symbol:go:example.com/fx/partial_write:CommitSwallow", RepoAbs: dir, Depth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Result.Suspicious) == 0 {
		t.Fatalf("应标出错误被覆盖/吞掉的可疑点: %+v", res.Result)
	}
	if res.Result.Verdict != SeqVerdictUnverified {
		t.Errorf("可疑形态必须 unverified（需人工确认）: %+v", res.Result)
	}
	joined := strings.Join(res.Result.Suspicious, ",")
	if !strings.Contains(joined, "flow.go:") {
		t.Errorf("可疑点应带 file:line 定位: %s", joined)
	}
}
