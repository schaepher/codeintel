//go:build integration

package integration

// todo22 §9.1 八个同步流程的入口源码（SourceA/B × 新建实体/新增子项/
// 编辑子项/编辑实体）。每项独立入口、独立存储方法；人工 FinishReview
// 次数为零（同步提交流程没有人工审核）。

// flowMatrixSync §9.1 八个同步流程（SourceA/B × 新建实体/新增子项/编辑子项/
// 编辑实体）。每项独立入口、独立存储方法，人工 FinishReview 次数为零。
const flowMatrixSync = `package flow

import (
	"context"

	"go-micro.dev/v2/client"
)

func (a *App) Flow21NewEntityA(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	if err := a.txInsert(ctx); err != nil {
		return err
	}
	ApproveTrace()
	return nil
}

func (a *App) Flow22NewChildA(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	if err := a.txInsertChild(ctx); err != nil {
		return err
	}
	ApproveTrace()
	return nil
}

func (a *App) Flow23EditChildA(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	if err := a.txUpdateChild(ctx); err != nil {
		return err
	}
	ApproveTrace()
	return nil
}

func (a *App) Flow24NewEntityB(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	if err := a.txInsert(ctx); err != nil {
		return err
	}
	ApproveTrace()
	return nil
}

func (a *App) Flow25NewChildB(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	if err := a.txInsertChild(ctx); err != nil {
		return err
	}
	ApproveTrace()
	return nil
}

func (a *App) Flow26EditChildB(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	if err := a.txUpdateChild(ctx); err != nil {
		return err
	}
	ApproveTrace()
	return nil
}

func (a *App) Flow27EditEntityA(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	if err := a.txUpdateEntity(ctx); err != nil {
		return err
	}
	ApproveTrace()
	return nil
}

func (a *App) Flow28EditEntityB(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	if err := a.txUpdateEntity(ctx); err != nil {
		return err
	}
	ApproveTrace()
	return nil
}
`
