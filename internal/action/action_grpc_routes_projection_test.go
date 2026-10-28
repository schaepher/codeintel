package action

// T16：GrpcRoutes 注册投影一次读取（多服务不逐服务查 registers_service 属性）；
// 同名但未注册的 helper 不成为路由。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// countingRoutesReader 统计注册属性查询次数（投影 vs 按服务），其余委托
// 真实仓储。
type countingRoutesReader struct {
	Reader
	projectionReads int
	perServiceReads int
}

func (c *countingRoutesReader) GetRouteRegisterNodes() ([]*domain.CodeEntity, error) {
	c.projectionReads++
	return c.Reader.GetRouteRegisterNodes()
}

func (c *countingRoutesReader) GetRegisterNode(svcName string) (*domain.CodeEntity, error) {
	c.perServiceReads++
	return c.Reader.GetRegisterNode(svcName)
}

func TestGrpcRoutesRegisterProjectionReadOnce(t *testing.T) {
	acts, dir := SeedGrpcRoutesRepo(t)
	counted := &countingRoutesReader{Reader: acts.repo}
	out, err := New(counted).GrpcRoutes(dir)
	if err != nil {
		t.Fatalf("GrpcRoutes: %v", err)
	}
	if len(out.Services) < 2 {
		t.Fatalf("服务数 = %d; want >=2（多服务才能验证共用投影）", len(out.Services))
	}
	if counted.projectionReads != 1 {
		t.Errorf("注册投影读取次数 = %d, want 1", counted.projectionReads)
	}
	if counted.perServiceReads != 0 {
		t.Errorf("仍按服务查属性 %d 次（应只读一次投影）", counted.perServiceReads)
	}
}

// 同名但未注册的 helper（无 registers_service 属性、不是 grpc_service 节点）
// 不得成为路由。
func TestGrpcRoutesIgnoresUnregisteredHelper(t *testing.T) {
	acts, dir := SeedGrpcRoutesRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := sqlite.NewRepo(db)
	if _, err := repo.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m/grpc:RegisterUnusedHandler", Kind: domain.KindFunction,
			Name: "RegisterUnusedHandler", FilePath: "grpc/unused.go", LineStart: 5},
	}, nil, nil); err != nil {
		t.Fatalf("save helper: %v", err)
	}
	out, err := acts.GrpcRoutes(dir)
	if err != nil {
		t.Fatalf("GrpcRoutes: %v", err)
	}
	for _, s := range out.Services {
		if s.Name == "Unused" || s.Name == "RegisterUnusedHandler" {
			t.Fatalf("未注册 helper 不应成为路由: %+v", s)
		}
	}
}
