//go:build integration

package integration

// todo27 T09：go-micro 生成桩源码回退与准确路由（真实 init → CLI）。
//
// 覆盖：客户端桩 → 服务端注册实现续接（同形客户端/重复调用按调用点身份）；
// 旧索引（缺 rpc_service/registers_service 属性与 grpc_impl 边）→ 源码回退
// （Register<Handler>Handler 的真实调用 + 注册实参工厂返回类型收窄）；
// 未被调用的 Register helper 不是路由。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// t09Fixture 写入完整夹具（go-micro 本地 stub + 生成桩 + 服务端）。
func t09Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                     t09FileGoMod,
		"microstub/go.mod":           t09FileMicrostubGoMod,
		"microstub/client/client.go": t09FileMicrostubClientClientGo,
		"microstub/server/server.go": t09FileMicrostubServerServerGo,
		"microstub/micro.go":         t09FileMicrostubMicroGo,
		"pb/proto.go":                t09FilePbProtoGo,
		"app.go":                     t09FileAppGo,
	}
	for rel, body := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), body)
	}
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func t09SeqOut(t *testing.T, dir, entry string) string {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/microflow:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	return out
}

// TestT09RPCContinuationIdentity：同形客户端/重复调用按调用点身份续接。
func TestT09RPCContinuationIdentity(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t09Fixture(t)
	base := t09SeqOut(t, dir, "Entry")
	if !strings.Contains(base, "id=symbol:go:example.com/microflow:(AccountServer).Apply verdict=corroborated") ||
		!strings.Contains(base, "id=symbol:go:example.com/microflow:SaveAudit verdict=") {
		t.Errorf("AccountService 客户端应续接到 AccountServer.Apply\n%s", base)
	}
	other := t09SeqOut(t, dir, "EntryOther")
	if !strings.Contains(other, "(OtherServer).Apply") || !strings.Contains(other, "SaveOtherAudit") {
		t.Errorf("同形 OtherServiceClient 应续接到 OtherServer（不按方法短名串源）\n%s", other)
	}
	if strings.Contains(other, "(AccountServer).Apply") {
		t.Errorf("OtherService 不得续接到 AccountServer\n%s", other)
	}
	repeat := t09SeqOut(t, dir, "EntryRepeat")
	if got := strings.Count(repeat, "(AccountServer).Apply verdict=corroborated"); got != 2 {
		t.Errorf("重复调用应各自保留身份（2 次，got %d）\n%s", got, repeat)
	}
}

// TestT09UncalledRegisterHelper：未被调用的 Register helper 不是路由。
func TestT09UncalledRegisterHelper(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t09Fixture(t)
	out := t09SeqOut(t, dir, "EntryGhost")
	if !strings.Contains(out, "rpc_registration_missing") {
		t.Errorf("未注册服务应 rpc_registration_missing\n%s", out)
	}
	if strings.Contains(out, "(GhostServer).Apply") {
		t.Errorf("未被调用的 Register helper 不得当路由\n%s", out)
	}
}

// TestT09OldIndexSourceFallback：旧索引（缺注册属性/grpc_impl 边）→ 源码回退。
func TestT09OldIndexSourceFallback(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t09Fixture(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE nodes SET properties = json_remove(properties,
		'$.rpc_service', '$.registers_service', '$.rpc_client_service') WHERE properties IS NOT NULL`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM edges WHERE kind = 'grpc_impl'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	out := t09SeqOut(t, dir, "Entry")
	if !strings.Contains(out, "(AccountServer).Apply") {
		t.Errorf("旧索引应经源码回退解析注册实现\n%s", out)
	}
	if !strings.Contains(out, "SaveAudit") {
		t.Errorf("旧索引回退后应展开服务端方法内部调用\n%s", out)
	}
}
