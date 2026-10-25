package cli

// todo10-1：reindex 的 --adapter-timeout 校验必须在**任何库操作之前**。
// 原实现 `fs.Duration(...)` 未取回值（透传给 init），而 reindex 先
// `sqlite.Open` + 可能的 `RebuildDerivedSchema` 再调 cmdInit——非法时长
// （0/负）会先重置派生图表，违反"非法参数不得修改现有索引与人工数据"。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestParseReindexArgsAdapterTimeout：参数解析 + 校验（零值/负值拒绝，
// 合法值取回）。
func TestParseReindexArgsAdapterTimeout(t *testing.T) {
	cases := []struct {
		args    []string
		want    time.Duration
		wantErr bool
	}{
		{[]string{"--repo", ".", "--adapter-timeout", "5m"}, 5 * time.Minute, false},
		{[]string{"--repo", ".", "--adapter-timeout=90s"}, 90 * time.Second, false},
		{[]string{"--repo", "."}, 0, false}, // 0 = 用默认值（未显式传）
		{[]string{"--adapter-timeout", "0s"}, 0, true},
		{[]string{"--adapter-timeout", "-1s"}, 0, true},
		{[]string{"--adapter-timeout=0"}, 0, true},
	}
	for _, c := range cases {
		repoPath, timeout, err := parseReindexArgs(c.args)
		if c.wantErr {
			if err == nil {
				t.Errorf("args=%v 应报错（timeout=%s repo=%q）", c.args, timeout, *repoPath)
			}
			continue
		}
		if err != nil {
			t.Errorf("args=%v 不应报错: %v", c.args, err)
			continue
		}
		if timeout != c.want {
			t.Errorf("args=%v timeout = %s; want %s", c.args, timeout, c.want)
		}
	}
}

// TestReindexInvalidAdapterTimeoutTouchesNothing：非法时长 → 非零退出，
// 且**不改动已有索引/人工数据**（文件字节级不变），也不新建 .codeintel。
func TestReindexInvalidAdapterTimeoutTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 预置索引 + 人工规则（relation_rules 是"必须保留"的人工数据）
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.NewRepo(db).AddRelationRule(domain.RelationRule{
		FromTable: "a_tab", FromCol: "b_id", ToTable: "b_tab", ToCol: "id",
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	dbPath := filepath.Join(dir, ".codeintel", "codeintel.db")
	before := hashFile(t, dbPath)

	for _, bad := range []string{"0s", "-5s"} {
		var code int
		stderr := captureStderr(func() {
			code = cmdReindex(context.Background(), []string{"--repo", dir, "--adapter-timeout", bad})
		})
		if code == 0 {
			t.Errorf("--adapter-timeout %s 应非零退出", bad)
		}
		if code != 2 {
			t.Errorf("--adapter-timeout %s 退出码 = %d; want 2（用法错误）", bad, code)
		}
		if !strings.Contains(stderr, "--adapter-timeout") || !strings.Contains(stderr, "必须为正时长") {
			t.Errorf("--adapter-timeout %s 错误信息应指出参数: %q", bad, stderr)
		}
		if after := hashFile(t, dbPath); after != before {
			t.Fatalf("--adapter-timeout %s 非法却改动了索引（人工数据可能被重置）", bad)
		}
	}

	// 无索引的目录：非法时长不得创建 .codeintel
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := cmdReindex(context.Background(), []string{"--repo", empty, "--adapter-timeout", "0s"})
	if code == 0 {
		t.Error("空目录非法时长也应非零退出")
	}
	if _, err := os.Stat(filepath.Join(empty, ".codeintel")); err == nil {
		t.Error("非法参数不得创建 .codeintel（库操作应在校验之后）")
	}
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
