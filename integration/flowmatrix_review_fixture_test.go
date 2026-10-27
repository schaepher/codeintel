//go:build integration

package integration

// todo22 §9.2 二十个后续流程形态（Web/App/Partner App/宿主/支付/API），
// 复用 flowmatrix 的共享后端链（检查 → trace → RPC → 事务存储 → 装饰器），
// 并显式写明审核规则：
//   - 资料节点人工 FinishReview 一次（FinishDataReview，node_id=101）
//   - 服务节点人工 FinishReview 一次（FinishServiceReview，node_id=202）
//   - 自动审核通过的节点不调用人工 FinishReview（AutoApprove 体内无 finish）
//   - 外部注册状态同步是自动步骤（SyncRegistry），不算第三次人工 finish
//   - 无审核配置的入口（APINewChildDirect）人工 finish=0

const flowMatrixReviewForms = `package flow

import (
	"context"

	"go-micro.dev/v2/client"
)

// ---- 入口分派：Web / App / API（后端共享链 = checksAndTrace + 事务持久化） ----

func (a *App) webSubmit(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	return a.txInsert(ctx)
}

func (a *App) appSubmit(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	return a.txInsert(ctx)
}

func (a *App) apiSubmit(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	return a.txInsert(ctx)
}

func (a *App) partnerSubmit(ctx context.Context, req Request, c client.Client) error {
	if err := checksAndTrace(ctx, req, c); err != nil {
		return err
	}
	return a.txInsert(ctx)
}

// ---- 审核规则（人工两次 / 自动审核不调人工） ----

func finishManualReviews() {
	FinishDataReview()
	FinishServiceReview()
}

func autoApproveData() {
	AutoApprove(DataNode)
}

func editEntityReview() {
	FinishServiceReview()
}

// ---- 01–03 Web：新实体 / 新服务 / 新子项 ----
// 提交 RPC → 申请持久化 → 资料审核 → 服务审核 → 完成

func (a *App) FlowWebNewEntity(ctx context.Context, req Request, c client.Client) error {
	if err := a.webSubmit(ctx, req, c); err != nil {
		return err
	}
	finishManualReviews()
	ApproveTrace()
	return nil
}

func (a *App) FlowWebNewService(ctx context.Context, req Request, c client.Client) error {
	if err := a.webSubmit(ctx, req, c); err != nil {
		return err
	}
	if err := a.txSaveService(ctx); err != nil {
		return err
	}
	finishManualReviews()
	ApproveTrace()
	return nil
}

func (a *App) FlowWebNewChild(ctx context.Context, req Request, c client.Client) error {
	if err := a.webSubmit(ctx, req, c); err != nil {
		return err
	}
	if err := a.txInsertChild(ctx); err != nil {
		return err
	}
	finishManualReviews()
	ApproveTrace()
	return nil
}

// ---- 04–06 App：新实体 / 新服务 / 新子项（入口与 Web 区分，共享后端链） ----

func (a *App) FlowAppNewEntity(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	finishManualReviews()
	ApproveTrace()
	return nil
}

func (a *App) FlowAppNewService(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	if err := a.txSaveService(ctx); err != nil {
		return err
	}
	finishManualReviews()
	ApproveTrace()
	return nil
}

func (a *App) FlowAppNewChild(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	if err := a.txInsertChild(ctx); err != nil {
		return err
	}
	finishManualReviews()
	ApproveTrace()
	return nil
}

// ---- 07–09 Partner App：提交处理器 + 来源分派（自动/人工审核分支） ----

func (a *App) FlowPartnerNewEntity(ctx context.Context, req Request, c client.Client) error {
	if err := a.partnerSubmit(ctx, req, c); err != nil {
		return err
	}
	// 来源分派：SourceA 自动审核（不调人工 FinishReview）；否则人工两次
	if req.Source == SourceA {
		autoApproveData()
	} else {
		finishManualReviews()
	}
	ApproveTrace()
	return nil
}

func (a *App) FlowPartnerNewService(ctx context.Context, req Request, c client.Client) error {
	if err := a.partnerSubmit(ctx, req, c); err != nil {
		return err
	}
	if err := a.txSaveService(ctx); err != nil {
		return err
	}
	if req.Source == SourceA {
		autoApproveData()
	} else {
		finishManualReviews()
	}
	ApproveTrace()
	return nil
}

func (a *App) FlowPartnerNewChild(ctx context.Context, req Request, c client.Client) error {
	if err := a.partnerSubmit(ctx, req, c); err != nil {
		return err
	}
	if err := a.txInsertChild(ctx); err != nil {
		return err
	}
	if req.Source == SourceA {
		autoApproveData()
	} else {
		finishManualReviews()
	}
	ApproveTrace()
	return nil
}

// ---- 10 编辑子项：审核完成与基本资料实际更新分别证明 ----

func (a *App) FlowEditChild(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	FinishDataReview()
	return a.txUpdateChild(ctx)
}

// ---- 11 App 新银行账户：自动批准/人工审核两条路径 + 状态与主数据分别证明 ----

func (a *App) FlowAppNewBankAccount(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	if req.Source == SourceA {
		autoApproveData()
	} else {
		finishManualReviews()
	}
	if err := a.txSaveAccount(ctx); err != nil {
		return err
	}
	return a.txUpdateEntityState(ctx)
}

// ---- 12–13 编辑主体（App / Web）：共享审核 + 实际更新 ----

func (a *App) FlowAppEditEntity(ctx context.Context, req Request, c client.Client) error {
	if err := a.appSubmit(ctx, req, c); err != nil {
		return err
	}
	editEntityReview()
	return a.txUpdateEntity(ctx)
}

func (a *App) FlowWebEditEntity(ctx context.Context, req Request, c client.Client) error {
	if err := a.webSubmit(ctx, req, c); err != nil {
		return err
	}
	editEntityReview()
	return a.txUpdateEntity(ctx)
}
`
