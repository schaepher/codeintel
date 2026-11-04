//go:build integration

package integration

// todo29 T09b：具体注册参数的 RPC 源码续接（真实 init → CLI）。

import (
	"strings"
	"testing"
)

// 具体复合字面量注册：源码回退解析真实注册 helper → 服务端方法/DI/AOP/回调。
func TestT29bConcreteRegistration(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t29bFixture(t, t29bRegisterOK)
	for _, args := range [][2]string{{"20", ""}, {"1", "32"}} {
		var extra []string
		if args[1] != "" {
			extra = []string{"--continue-segments", args[1]}
		}
		got := t29bQuery(t, dir, args[0], extra...)
		if got.Stop != nil {
			t.Fatalf("depth=%s stop=%v，期望无停止\n%s", args[0], got.Stop, t29bDump(t, dir, args[0], extra...))
		}
		if got.ServerNodes != 1 || got.Save != 1 || got.Cleanup != 1 {
			t.Fatalf("depth=%s server=%d Save=%d Cleanup=%d, want 1/1/1\n%s",
				args[0], got.ServerNodes, got.Save, got.Cleanup, t29bDump(t, dir, args[0], extra...))
		}
		if !strings.HasSuffix(got.RCPSymbol, ":(Handler).Submit") {
			t.Fatalf("depth=%s 服务端方法 = %q, want Handler.Submit", args[0], got.RCPSymbol)
		}
		if got.RegisterFile == "" || got.RegisterLine == 0 {
			t.Fatalf("depth=%s 注册源码位置缺失: %s:%d", args[0], got.RegisterFile, got.RegisterLine)
		}
		if got.CleanupExec != "defer" {
			t.Fatalf("depth=%s Cleanup 应标注 defer（got %q）", args[0], got.CleanupExec)
		}
		want := "FactoryFirst,FactorySecond,BeforeSecond,BeforeFirst,Save,AfterFirst,AfterSecond"
		if strings.Join(got.Order, ",") != want {
			t.Fatalf("depth=%s 调用顺序 = %v, want %s", args[0], got.Order, want)
		}
	}
}

// 反例：删除真实注册调用 / 注册实参不实现 handler 接口 → 不沿用原路由。
func TestT29bNegatives(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	for name, reg := range map[string]string{
		"uncalled": t29bRegisterMissing,
		"bad-type": t29bRegisterBadType,
	} {
		dir := t29bFixture(t, reg)
		got := t29bQuery(t, dir, "20")
		if got.Stop == nil {
			t.Fatalf("%s 应保留注册缺失/解析诊断", name)
		}
		if got.ServerNodes != 0 || got.Save != 0 {
			t.Fatalf("%s 不得沿用原服务路由: server=%d Save=%d", name, got.ServerNodes, got.Save)
		}
	}
}
