package sqlite

// todo21-14.2/14.4：包清单与递归 CALLS 查询的取消语义。
//
// 两个回归都要求"调用本身结束、连接可复用"——不能用 select 外层 goroutine
// 返回伪装后台 SQL 已停止。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestPackagesContextCancelsConnectionWait：唯一连接被占用时，
// GetPackagesContext 必须随 ctx 结束（而不是等到连接空闲），
// 且取消不得污染后续查询。
func TestPackagesContextCancelsConnectionWait(t *testing.T) {
	r := newTestRepo(t)
	conn, err := r.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := r.GetPackagesContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query must stop waiting for the sole connection: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetPackages(); err != nil {
		t.Fatalf("cancellation poisoned the next query: %v", err)
	}
}

// TestCalleesContextInterruptsRecursiveSQL：深递归（两节点双向环 + 深度
// 1000000）必须被 25ms 预算实际中断（modernc 驱动在返回首条结果后撤销
// 取消监听，故递归体物化），随后同一连接立即可复用。
func TestCalleesContextInterruptsRecursiveSQL(t *testing.T) {
	r := newTestRepo(t)
	a, b := domain.CanonicalID("symbol:go:example.com/m:a"), domain.CanonicalID("symbol:go:example.com/m:b")
	save(t, r, []*domain.CodeEntity{node(string(a), "function", "a", "a.go"), node(string(b), "function", "b", "a.go")}, nil)
	save(t, r, nil, []*domain.Fact{
		{SourceID: a, TargetID: b, Kind: domain.FactCalls, Confidence: 1},
		{SourceID: b, TargetID: a, Kind: domain.FactCalls, Confidence: 1},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := r.GetCalleesContext(ctx, a, 1000000, .8); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("recursive SQL must be interrupted: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("25ms query cancellation took %s", elapsed)
	}
	if facts, err := r.GetCallees(a, 1, .8); err != nil || len(facts) != 1 {
		t.Fatalf("connection must be reusable after SQL stopped: %v %v", facts, err)
	}
}
