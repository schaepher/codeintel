package cli

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDebugDiffKeys(t *testing.T) {
	var tpl map[string]any
	yaml.Unmarshal([]byte(configExample), &tpl)
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	os.WriteFile(path, []byte("project:\n  description: 我的项目\nseq:\n  stop_packages: [a]\nai:\n  fill: off\n"), 0o644)
	b, _ := os.ReadFile(path)
	var cur map[string]any
	yaml.Unmarshal(b, &cur)
	var missing [][]string
	diffKeys("", tpl, cur, &missing)
	for _, m := range missing {
		t.Logf("missing: %v", m)
	}
}
