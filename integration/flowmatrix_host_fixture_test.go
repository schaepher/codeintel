//go:build integration

package integration

// todo22 §9.2（续）：宿主/支付/API 的流程形态（14–20），与
// flowmatrix_review_fixture_test.go 同一 source 文件集合的一部分。

const flowMatrixHostForms = `package flow

import (
	"context"

	"go-micro.dev/v2/client"
)

// ---- 14–16 三种宿主的新银行账户：宿主专用处理器 + 共享处理器 + 审核分支 ----

func (a *App) hostAHandler(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	autoApproveData()
	return nil
}

func (a *App) hostBHandler(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	finishManualReviews()
	return nil
}

func (a *App) hostCHandler(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	FinishDataReview()
	return nil
}

func (a *App) hostApply(ctx context.Context) error {
	return a.txSaveAccount(ctx)
}

func (a *App) FlowHostANewBankAccount(ctx context.Context, req Request, c client.Client) error {
	if err := a.hostAHandler(ctx, req, c); err != nil {
		return err
	}
	return a.hostApply(ctx)
}

func (a *App) FlowHostBNewBankAccount(ctx context.Context, req Request, c client.Client) error {
	if err := a.hostBHandler(ctx, req, c); err != nil {
		return err
	}
	return a.hostApply(ctx)
}

func (a *App) FlowHostCNewBankAccount(ctx context.Context, req Request, c client.Client) error {
	if err := a.hostCHandler(ctx, req, c); err != nil {
		return err
	}
	return a.hostApply(ctx)
}

// ---- 17 支付 App 新实体：条件自动审核 + 人工资料审核 + 注册状态同步 ----

func (a *App) FlowPaymentNewEntity(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	if req.Source == SourceA {
		autoApproveData()
	}
	FinishDataReview()
	SyncRegistry()
	return nil
}

// ---- 18 宿主转入的新服务：提交 → 资料审核 → 外部注册/回调 → 申请完成 ----

func (a *App) hostTransferSubmit(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	return a.txSaveService(ctx)
}

func (a *App) FlowHostTransferNewService(ctx context.Context, req Request, c client.Client) error {
	if err := a.hostTransferSubmit(ctx, req, c); err != nil {
		return err
	}
	FinishDataReview()
	SyncRegistry()
	ApproveTrace()
	return nil
}

// ---- 19–20 API：有审核流程 / 直接主数据（不虚构审核节点） ----

func (a *App) FlowAPINewEntityReview(ctx context.Context, req Request, c client.Client) error {
	if err := a.apiSubmit(ctx, req, c); err != nil {
		return err
	}
	finishManualReviews()
	return nil
}

func (a *App) FlowAPINewChildDirect(ctx context.Context, req Request, c client.Client) error {
	if err := a.apiSubmit(ctx, req, c); err != nil {
		return err
	}
	return a.txInsertChild(ctx)
}
`
