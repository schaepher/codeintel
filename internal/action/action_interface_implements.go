package action

// T16：接口 implements 查询的 action 入口（CLI/MCP/serve 共用同一查询）。
//
// 语义：准确接口 canonical ID → 实现目标集合（只取 implements 边，稳定
// 排序，未知接口空集）；取消经 ctx 透传到仓储层（不返回截断结果）。

import (
	"context"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// InterfaceImplements 接口的全部实现目标（canonical ID 字符串，稳定排序）。
func (a *Actions) InterfaceImplements(ctx context.Context, ifaceID string) ([]string, error) {
	logger := zap.L()
	logger.Info("enter (Actions).InterfaceImplements", zap.String("iface", ifaceID))
	defer logger.Info("exit (Actions).InterfaceImplements")
	if a == nil || a.repo == nil {
		return nil, nil
	}
	ids, err := a.repo.GetInterfaceImplements(ctx, ifaceID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out, nil
}

// InterfaceImplementsIDs 同 InterfaceImplements，返回 CanonicalID 切片
// （action 内部调用方用，避免字符串来回转换）。
func (a *Actions) InterfaceImplementsIDs(ctx context.Context, ifaceID domain.CanonicalID) ([]domain.CanonicalID, error) {
	logger := zap.L()
	logger.Info("enter (Actions).InterfaceImplementsIDs", zap.String("iface", string(ifaceID)))
	defer logger.Info("exit (Actions).InterfaceImplementsIDs")
	if a == nil || a.repo == nil {
		return nil, nil
	}
	return a.repo.GetInterfaceImplements(ctx, string(ifaceID))
}
