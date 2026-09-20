package ssa

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// indexFixtureFullOrigins 同 indexFixtureFull，额外收集 Q161 origins
// （emitSummaries 发射的 Item.Origins）。
func indexFixtureFullOrigins(t *testing.T, files map[string]string) ([]*domain.CodeEntity, []*domain.Fact, []*domain.FunctionFieldSummary, []*domain.SummaryOrigin) {
	t.Helper()
	return collectFixtureIndex(t, files, 0)
}

// collectFixtureIndex 唯一实现（workers=0 即 Adapter 默认：串行）。
// Q252e：并发度参与产物等价性验证（workers 只该影响速度，不该影响图）。
func collectFixtureIndex(t *testing.T, files map[string]string, workers int) ([]*domain.CodeEntity, []*domain.Fact, []*domain.FunctionFieldSummary, []*domain.SummaryOrigin) {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		writeFile(t, filepath.Join(dir, path), content)
	}
	var nodes []*domain.CodeEntity
	var facts []*domain.Fact
	var summaries []*domain.FunctionFieldSummary
	var origins []*domain.SummaryOrigin
	// Q169：emit 回调并发安全（按包并发后多 goroutine 同时调 emit）
	var emitMu sync.Mutex
	adapter := &Adapter{}
	if workers > 0 {
		adapter.SetWorkers(workers)
	}
	repo := &domain.Repository{Path: dir, Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	err = adapter.Index(context.Background(), repo, pkgs, func(item domain.Item) error {
		emitMu.Lock()
		defer emitMu.Unlock()
		if item.Node != nil {
			nodes = append(nodes, item.Node)
		}
		if item.Fact != nil {
			facts = append(facts, item.Fact)
		}
		if item.Summary != nil {
			summaries = append(summaries, item.Summary)
		}
		if item.Origins != nil {
			origins = append(origins, item.Origins...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	return nodes, facts, summaries, origins
}
