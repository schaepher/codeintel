package sqlite

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// migrate 幂等 schema + 列变更重建迁移（Q16）。
func (r *Registry) migrate() error {
	if _, err := r.db.Exec(registrySchema); err != nil {
		return fmt.Errorf("registry schema: %w", err)
	}

	actual, err := tableCols(r.db, "repos")
	if err != nil {
		return err
	}
	hasAll := true
	for _, c := range registryCols {
		if !actual[c] {
			hasAll = false
			break
		}
	}
	onlyExpected := true
	for c := range actual {
		found := false
		for _, want := range registryCols {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			onlyExpected = false
			break
		}
	}
	if hasAll && onlyExpected {
		_, _ = r.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", RegistrySchemaVersion))
		return nil
	}

	if err := r.rebuildMigrate(); err != nil {
		return err
	}
	_, _ = r.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", RegistrySchemaVersion))
	return nil
}

// rebuildMigrate 重建 repos 表并迁移共有列数据（Q16 列变更不丢台账）。
func (r *Registry) rebuildMigrate() error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	actual, err := tableCols(r.db, "repos")
	if err != nil {
		return err
	}
	var shared, dropCols []string
	for _, c := range registryCols {
		if actual[c] {
			shared = append(shared, c)
		} else {
			dropCols = append(dropCols, c)
		}
	}
	if _, err := tx.Exec("ALTER TABLE repos RENAME TO repos_old"); err != nil {
		return fmt.Errorf("rename old: %w", err)
	}
	if _, err := tx.Exec(registrySchema); err != nil {
		return fmt.Errorf("create new: %w", err)
	}
	if len(shared) > 0 {
		cols := joinCols(shared)
		if _, err := tx.Exec(fmt.Sprintf("INSERT INTO repos(%s) SELECT %s FROM repos_old", cols, cols)); err != nil {
			return fmt.Errorf("migrate data: %w", err)
		}
	}
	if _, err := tx.Exec("DROP TABLE repos_old"); err != nil {
		return fmt.Errorf("drop old: %w", err)
	}
	_ = dropCols
	return tx.Commit()
}
func joinCols(cols []string) string {
	s := ""
	for i, c := range cols {
		if i > 0 {
			s += ", "
		}
		s += c
	}
	return s
}

// tableCols 返回表实际列集合（PRAGMA table_xinfo——含生成列）。
func tableCols(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_xinfo(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		var hidden int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk, &hidden); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}
