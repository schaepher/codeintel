//go:build integration

package integration

// todo22 §9 夹具核心源码：§8 的装饰器/事务回调/存储结构 + trace 提交/批准、
// 准确 RPC 注册（go-micro）、DI/AOP 字段注入、§9.2 审核完成规则与检查清单。

// flowMatrixCore §8 结构 + 本矩阵需要的存储/审核/DI/RPC 事实。
const flowMatrixCore = `package flow

import (
	"context"
	"database/sql"

	"example.com/flowmatrix/pb"
	"example.com/tx"
	micro "go-micro.dev/v2"
	"go-micro.dev/v2/client"
)

type Source int32

const (
	SourceA Source = 7
	SourceB Source = 19
)

type Request struct {
	Source Source
	ID     int64
}

type Handler func(context.Context) error
type Decorator func(context.Context, Handler) Handler

// 存储 API（事务回调终点；按操作用不同方法，验收才区分得开）
type Store struct{ DB *sql.DB }

func (s *Store) Insert(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO records(id) VALUES (?)", 1)
	return err
}

func (s *Store) InsertChild(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO children(id, parent_id) VALUES (?, ?)", 1, 2)
	return err
}

func (s *Store) UpdateChild(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE children SET name = ? WHERE id = ?", "x", 1)
	return err
}

func (s *Store) UpdateEntity(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE records SET name = ? WHERE id = ?", "x", 1)
	return err
}

func (s *Store) SaveAccount(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO accounts(id) VALUES (?)", 1)
	return err
}

func (s *Store) UpdateEntityState(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE entity_states SET state = ? WHERE id = ?", 1, 1)
	return err
}

func (s *Store) SaveService(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO services(id) VALUES (?)", 1)
	return err
}

// DI/AOP：接口字段注入 + 事务包装器（不跳过 wrapper 直接接业务方法）
type Applier interface {
	Insert(context.Context) error
	InsertChild(context.Context) error
	UpdateChild(context.Context) error
	UpdateEntity(context.Context) error
	SaveAccount(context.Context) error
	UpdateEntityState(context.Context) error
	SaveService(context.Context) error
}

// 事务包装器持有**业务实现**（不是同一接口——否则包装器自身也满足接口，
// 内部委托无法静态消歧；真实 AOP 框架的 wrapper 也是包住原实现）
type storeAOP struct {
	inner *Store
}

func (a *storeAOP) Insert(ctx context.Context) error {
	return a.inner.Insert(ctx)
}

func (a *storeAOP) InsertChild(ctx context.Context) error {
	return a.inner.InsertChild(ctx)
}

func (a *storeAOP) UpdateChild(ctx context.Context) error {
	return a.inner.UpdateChild(ctx)
}

func (a *storeAOP) UpdateEntity(ctx context.Context) error {
	return a.inner.UpdateEntity(ctx)
}

func (a *storeAOP) SaveAccount(ctx context.Context) error {
	return a.inner.SaveAccount(ctx)
}

func (a *storeAOP) UpdateEntityState(ctx context.Context) error {
	return a.inner.UpdateEntityState(ctx)
}

func (a *storeAOP) SaveService(ctx context.Context) error {
	return a.inner.SaveService(ctx)
}

func WrapStore(inner *Store) Applier {
	return &storeAOP{inner: inner}
}

type App struct {
	// @inject store
	Store Applier
}

func Register(key string, impl any) {}

func init() {
	Register("store", WrapStore(&Store{}))
}

// trace 提交/批准
func SaveTrace() {}
func ApproveTrace() {}

// §9.2 人工审核完成（资料节点 / 服务节点，node_id 不同）
type ReviewNode struct {
	ID   int64
	Kind string
}

func FinishReview(n ReviewNode) {}

func FinishDataReview() {
	FinishReview(ReviewNode{ID: 1, Kind: "data"})
}

func FinishServiceReview() {
	FinishReview(ReviewNode{ID: 2, Kind: "service"})
}

// 自动审核通过（不调用人工 FinishReview）与外部注册状态同步（自动步骤）
func AutoApprove(n ReviewNode) {}
func SyncRegistry()            {}

// §8 原样的装饰器与事务回调
func Setup() {}
func BeforeFirst() {}
func AfterFirst() {}
func BeforeSecond() {}
func AfterSecond() {}

func MakeFirst(ctx context.Context) Decorator {
	Setup()
	return First
}

func First(ctx context.Context, f Handler) Handler {
	return func(inner context.Context) error {
		BeforeFirst()
		err := f(inner)
		if err != nil {
			return err
		}
		AfterFirst()
		return nil
	}
}

func Second(ctx context.Context, f Handler) Handler {
	if ctx.Value("bypass") != nil {
		return f
	}
	return func(inner context.Context) error {
		BeforeSecond()
		err := f(inner)
		if err != nil {
			return err
		}
		AfterSecond()
		return nil
	}
}

func Execute(ctx context.Context, process Handler, wrappers ...Decorator) error {
	for _, wrapper := range wrappers {
		process = wrapper(ctx, process)
	}
	return tx.Run(ctx, func(inner context.Context) error { return process(inner) })
}

func Check1() {}
func Check2() {}
func Check3() {}
func Check4() {}
func Check5() {}
func Check6() {}
func Check7() {}

// 准确 RPC 注册：客户端只请求 example.flow.AccountService.Apply
func ApplyRemote(ctx context.Context, c client.Client) error {
	cli := pb.NewAccountServiceClient(c)
	_, err := cli.Apply(ctx, &pb.ApplyRequest{})
	return err
}

type AccountServer struct{}

func (h *AccountServer) Apply(ctx context.Context, in *pb.ApplyRequest, out *pb.ApplyResponse) error {
	SyncRegistry()
	return nil
}

func SetupServices() {
	svc := micro.NewService(micro.Name("example.flow.AccountService"))
	pb.RegisterAccountServiceHandler(svc.Server(), new(AccountServer))
}

// 入口公共链：检查清单（SourceA 4 项 / SourceB 7 项，§8 期望）→ trace
// 提交 → RPC（服务端 Apply）。列表用 §8 的复合字面量 + append 形态（数据流
// 可证明；函数返回列表属于未支持形态）。
func checksAndTrace(ctx context.Context, req Request, c client.Client) error {
	checks := []func(){Check1, Check2, Check3, Check4}
	if int32(req.Source) == int32(SourceB) {
		checks = append(checks, Check5, Check6, Check7)
	}
	for _, check := range checks {
		check()
	}
	SaveTrace()
	return ApplyRemote(ctx, c)
}

// 存储写入按操作独立成方法（装饰器 + 事务回调 + DI/AOP 都在方法体内：
// 闭包在 App 方法里创建，接收者上下文可继承，字段注入才能证明到 wrapper/实现）
func (a *App) txInsert(ctx context.Context) error {
	return Execute(ctx, func(inner context.Context) error { return a.Store.Insert(inner) }, MakeFirst(ctx), Second)
}

func (a *App) txInsertChild(ctx context.Context) error {
	return Execute(ctx, func(inner context.Context) error { return a.Store.InsertChild(inner) }, MakeFirst(ctx), Second)
}

func (a *App) txUpdateChild(ctx context.Context) error {
	return Execute(ctx, func(inner context.Context) error { return a.Store.UpdateChild(inner) }, MakeFirst(ctx), Second)
}

func (a *App) txUpdateEntity(ctx context.Context) error {
	return Execute(ctx, func(inner context.Context) error { return a.Store.UpdateEntity(inner) }, MakeFirst(ctx), Second)
}

// 事务回调 + 非空装饰器；write 是本次操作的实际存储写入
func (a *App) txWith(ctx context.Context, write Handler) error {
	return Execute(ctx, write, MakeFirst(ctx), Second)
}
`

// 夹具路径/写入在 flowmatrix_write_test.go；§9.1 八个同步流程在
// flowmatrix_sync_fixture_test.go。
