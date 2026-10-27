//go:build integration

package integration

// todo22 §8 的公开脱敏夹具：`example.com/flow` 主模块 + `example.com/tx`
// 本地 replace 依赖。源码**逐字**照抄 todo22 第 8 节（含 4 空格缩进、单行
// type/const/func 声明）——所以 `(Store).Insert` 的声明位置固定为
// flow.go:15–18，符号源码输出（§6）可以断言逐字节相等。
//
// 用途：§6 的 --include-source 验收（本轮）以及后续 §2/§9 的流程矩阵
// （闭包/装饰器/事务回调/场景裁剪）共用同一份夹具，避免"每个用例一套
// 失真夹具"。

import (
	"os"
	"path/filepath"
	"testing"
)

// flowFixtureGoMod 主模块 go.mod（todo22 §8 原样）。
const flowFixtureGoMod = `module example.com/flow

go 1.26.2

require example.com/tx v0.0.0

replace example.com/tx => ./tx
`

// flowFixtureTxGoMod 本地依赖模块 go.mod（分行保存，todo22 §8 原样）。
const flowFixtureTxGoMod = `module example.com/tx
go 1.26.2
`

// flowFixtureTxGo 事务辅助（todo22 §8 原样）。
const flowFixtureTxGo = `package tx
import "context"
func Run(ctx context.Context, f func(context.Context) error) error { return f(ctx) }
`

// flowFixtureGo 主源码（todo22 §8 原样；`(Store).Insert` 在 15–18 行）。
const flowFixtureGo = `package flow

import (
    "context"
    "database/sql"
    "example.com/tx"
)

type Source int32
const (SourceA Source = 7; SourceB Source = 19)
type Request struct { Source Source }
type Handler func(context.Context) error
type Decorator func(context.Context, Handler) Handler
type Store struct { DB *sql.DB }
func (s *Store) Insert(ctx context.Context) error {
    _, err := s.DB.ExecContext(ctx, "INSERT INTO records(id) VALUES (?)", 1)
    return err
}
func Setup() {}; func BeforeFirst() {}; func AfterFirst() {}
func BeforeSecond() {}; func AfterSecond() {}
func Check1() {}; func Check2() {}; func Check3() {}; func Check4() {}
func Check5() {}; func Check6() {}; func Check7() {}
func SaveTrace() {}; func ApproveTrace() {}; func MarkInactive() {}
func IsRegionX() bool { return false }
func MakeFirst(ctx context.Context) Decorator { Setup(); return First }
func First(ctx context.Context, f Handler) Handler {
    return func(inner context.Context) error {
        BeforeFirst(); err := f(inner)
        if err != nil { return err }
        AfterFirst(); return nil
    }
}
func Second(ctx context.Context, f Handler) Handler {
    if ctx.Value("bypass") != nil { return f }
    return func(inner context.Context) error {
        BeforeSecond(); err := f(inner)
        if err != nil { return err }
        AfterSecond(); return nil
    }
}
func Execute(ctx context.Context, process Handler, wrappers ...Decorator) error {
    for _, wrapper := range wrappers { process = wrapper(ctx, process) }
    return tx.Run(ctx, func(inner context.Context) error { return process(inner) })
}
func Entry(ctx context.Context, req Request, store *Store) error {
    checks := []func(){Check1, Check2, Check3, Check4}
    if int32(req.Source) == int32(SourceB) {
        checks = append(checks, Check5, Check6, Check7)
    }
    for _, check := range checks { check() }
    if req.Source == SourceA && IsRegionX() { MarkInactive() }
    SaveTrace()
    err := Execute(ctx, func(inner context.Context) error {
        return store.Insert(inner)
    }, MakeFirst(ctx), Second)
    if err != nil { return err }
    ApproveTrace(); return nil
}
`

// writeFlowFixture 在临时目录写入 todo22 §8 夹具，返回主模块目录。
func writeFlowFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	main := filepath.Join(root, "flow")
	if err := os.MkdirAll(filepath.Join(main, "tx"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(main, "go.mod"), flowFixtureGoMod)
	writeFile(t, filepath.Join(main, "flow.go"), flowFixtureGo)
	writeFile(t, filepath.Join(main, "tx", "go.mod"), flowFixtureTxGoMod)
	writeFile(t, filepath.Join(main, "tx", "tx.go"), flowFixtureTxGo)
	return main
}
