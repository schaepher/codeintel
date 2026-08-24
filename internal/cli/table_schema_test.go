package cli

// R19 表字段类型自动填充测试：sqlite_master CREATE TABLE 解析 +
// 填充优先级（yaml > schema > gorm tag）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestParseCreateTableSchema：CREATE TABLE 解析列类型与默认值。
func TestParseCreateTableSchema(t *testing.T) {
	ddl := `CREATE TABLE build_metadata (
    build_id TEXT PRIMARY KEY,
    duration_ms INTEGER,
    status TEXT NOT NULL,
    timestamp INTEGER DEFAULT (strftime('%s', 'now')),
    degrade_stats TEXT,
    FOREIGN KEY(x) REFERENCES nodes(id)
)`
	schemas := parseCreateTableSchema(map[string]string{"build_metadata": ddl})
	cols := schemas["build_metadata"]
	if cols == nil {
		t.Fatal("应解析出 build_metadata 列")
	}
	if c := cols["build_id"]; c.Typ != "TEXT" {
		t.Errorf("build_id = %q, want TEXT", c.Typ)
	}
	if c := cols["duration_ms"]; c.Typ != "INTEGER" {
		t.Errorf("duration_ms = %q, want INTEGER", c.Typ)
	}
	if c := cols["timestamp"]; c.Typ != "INTEGER" || c.Def == "" {
		t.Errorf("timestamp = %+v, want INTEGER + DEFAULT", c)
	}
	if _, ok := cols["x"]; ok {
		t.Error("FOREIGN KEY 行不应算列")
	}
}

// TestMergeTableColumnsSchema：类型填充优先级 yaml > schema > ColType。
func TestMergeTableColumnsSchema(t *testing.T) {
	schemas := map[string]map[string]schemaCol{
		"t": {
			"a": {Typ: "TEXT", Def: "''"},
			"b": {Typ: "INTEGER", Def: "0"},
			"c": {Typ: "REAL"},
		},
	}
	cols := []*domain.TableColumn{
		{Name: "t.a", ColType: "gorm_string"},
		{Name: "t.b"},
		{Name: "t.c", ColType: "gorm_float"},
	}
	yamlCols := []wikiTableColumn{
		{Name: "a", Type: "TEXT", Comment: "人工类型"},
	}
	rows := mergeTableColumnsWithSchema("t", cols, yamlCols, schemas, nil)
	byName := map[string]tableColRow{}
	for _, r := range rows {
		byName[r.name] = r
	}
	// a：yaml 优先（即使 schema 同 TEXT）
	if r := byName["a"]; r.typ != "TEXT" || r.comment != "人工类型" {
		t.Errorf("a = %+v, want yaml TEXT", r)
	}
	// b：无 yaml → schema INTEGER
	if r := byName["b"]; r.typ != "INTEGER" || r.def != "0" {
		t.Errorf("b = %+v, want schema INTEGER", r)
	}
	// c：无 yaml/schema 类型缺 → 用 gorm tag？c 的 schema 有 REAL——
	// schema 优先于 ColType
	if r := byName["c"]; r.typ != "REAL" {
		t.Errorf("c = %+v, want schema REAL（优先 gorm tag）", r)
	}
}

// TestParseCreateTableSchema 兼容：列定义带引号/多空格。
func TestParseCreateTableSchemaQuoted(t *testing.T) {
	ddl := "CREATE TABLE nodes (\n\t\"id\"   TEXT PRIMARY KEY,\n\tproperties JSON\n)"
	schemas := parseCreateTableSchema(map[string]string{"nodes": ddl})
	cols := schemas["nodes"]
	if cols["id"].Typ != "TEXT" || cols["properties"].Typ != "JSON" {
		t.Errorf("引号/多空格解析失败: %+v", cols)
	}
}

var _ = strings.TrimSpace
