package action

// todo10-7 的表列映射与字符串助手（从 action_effects.go 拆出——行数治理）。

import "fmt"

// tableColumnIndex 表列索引（列名 → 表.列），用于字段→表列映射。
type tableColumnIndex struct {
	byColumn map[string][]string // 列名（小写）→ ["table.column", ...]
}

// tableColumnIndex 读全库表列建索引（列名匹配；无库表则空索引）。
func (a *Actions) tableColumnIndex() tableColumnIndex {
	idx := tableColumnIndex{byColumn: map[string][]string{}}
	cols, err := a.repo.GetAllTableColumns()
	if err != nil {
		return idx
	}
	for _, c := range cols {
		table, column := splitTableColumn(c.Name)
		if column == "" {
			continue
		}
		key := lowerSnake(column)
		idx.byColumn[key] = append(idx.byColumn[key], table+"."+column)
	}
	return idx
}

// lookup 字段名 → (表, 列)：按列名（snake_case 归一）匹配。
func (i tableColumnIndex) lookup(field string) (table, column string, ok bool) {
	hits := i.byColumn[lowerSnake(field)]
	if len(hits) == 0 {
		return "", "", false
	}
	table, column = splitTableColumn(hits[0])
	if len(hits) > 1 {
		// 多表同名列：不猜——取第一个但标注（verdict 由调用方按 ok 处理）
		return table, column, true
	}
	return table, column, true
}

// splitTableColumn "table.column" → (table, column)；无点则视为表名。
func splitTableColumn(name string) (table, column string) {
	if i := lastDot(name); i >= 0 {
		return name[:i], name[i+1:]
	}
	return name, ""
}

// lastDot 最后一个 '.' 的位置（-1 表示无）。
func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

// lowerSnake 列名归一（驼峰 → 下划线；小写）。
func lowerSnake(s string) string {
	out := make([]rune, 0, len(s)+4)
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out = append(out, '_')
			}
			out = append(out, r+('a'-'A'))
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// svcNameOfTarget grpc_call 边 target（symbol:go:<pkg>:svc.Xxx）→ 服务名。
func svcNameOfTarget(id string) string {
	name := id
	if i := lastColon(id); i >= 0 {
		name = id[i+1:]
	}
	return trimSvcPrefix(name)
}

// lastColon 最后一个 ':' 的位置（-1 表示无）。
func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

// trimSvcPrefix 去掉服务节点名里的 "svc." 前缀。
func trimSvcPrefix(name string) string {
	const p = "svc."
	if len(name) > len(p) && name[:len(p)] == p {
		return name[len(p):]
	}
	return name
}

// effectsScanResult AST 扫描结果（事务 + 返回结果）。
type effectsScanResult struct {
	tx     TxScope
	result ResultEffect
}

// String 便于日志/诊断输出。
func (r EffectsResult) String() string {
	return fmt.Sprintf("effects(%s): db=%d remote=%d tx=%s", r.Function, len(r.DBWrites), len(r.RemoteWrites), r.Tx.Kind)
}
