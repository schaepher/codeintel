// Package flow 主写入与追踪记录部分成功（todo10-8 夹具 4）。
//
// 场景：主数据写入成功后，追踪记录写入失败 → 返回错误。用来验证"按路径
// 输出副作用与返回结果"（todo10-7）：不能把错误返回解释成全部写入回滚
// （主写入已生效），也要能标注错误被后续赋值覆盖的可疑形态。
//
// 全部符号/数据中性。
package flow

// Store 存储抽象（中性：不绑定具体框架）。
type Store interface {
	Save(key string, value string) error
}

// Trace 追踪记录写入。
func Trace(st Store, key string, value string) error {
	return st.Save("trace:"+key, value)
}

// Ledger 主数据（字段写入素材——表列映射用）。
type Ledger struct {
	State   string
	TraceID string
}

// Commit 主写入成功 → 追踪记录失败 → 返回错误（部分成功）。
func Commit(st Store, key string, value string, l *Ledger) error {
	l.State = "committed" // 主写入（字段写入）
	if err := st.Save(key, value); err != nil {
		return err
	}
	return Trace(st, key, value)
}

// CommitOverwrite 主写入失败 → 错误被后续赋值覆盖（可疑：错误丢失）。
func CommitOverwrite(st Store, key string) error {
	var err error
	if e := st.Save(key, "v"); e != nil {
		err = e
	}
	_ = Trace(st, key, "")
	return err
}

// CommitSwallow 主写入失败后错误被覆盖，最终返回 nil（可疑：错误被吞）。
func CommitSwallow(st Store, key string) error {
	if err := st.Save(key, "v"); err != nil {
		err = nil
	}
	return nil
}
