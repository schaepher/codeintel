package ssa

import (
	"fmt"
	"go/token"
	"go/types"
	"io"
	"os"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

var stderr io.Writer = os.Stderr

// finishIndex 构建收尾（Q231 拆分自 Adapter.Index 尾部）：alias 分析 +
// 摘要/全局初始化/动态派发发射 + 内存释放。
func finishIndex(repo *domain.Repository, prog *ssa.Program, idents map[token.Pos]string,
	a *Adapter, typePkgs []*types.Package, fallbackAgg *fallbackAgg, emit domain.EmitFunc) error {
	logger := zap.L()
	logger.Debug("enter finishIndex")
	defer logger.Debug("exit finishIndex")
	if n := fallbackAgg.total(); n > 0 {
		fmt.Fprintf(stderr, "warning: %d 个字段访问静态类型解析失败（匿名 struct 等），已回退源码字面量路径\n", n)
		// R100：明细去重打印（相同 函数:路径 只一行 ×N；限量唯一条目）
		for i, d := range fallbackAgg.dump() {
			if i >= fallbackDetailLimit {
				break
			}
			fmt.Fprintf(stderr, "warning: 字段访问静态类型解析失败（回退源码字面量）: %s\n", d)
		}
	}
	aliasRes, err := computeAliases(repo, prog, idents, a.fd, emit)
	if err != nil {
		return fmt.Errorf("alias analysis: %w", err)
	}
	idents = nil

	if err := emitSummaries(a.fd, aliasRes, emit); err != nil {
		return err
	}
	a.fd, aliasRes = nil, nil

	if err := emitGlobalInit(repo, prog, emit); err != nil {
		return err
	}
	// P0-2：dispatch 相关包（注册点 ∪ 动态调用，emitDispatches 内合并
	// 去重）——增量补 Load 持久化
	dispatchPkgs, err := emitDispatches(repo, prog, typePkgs, emit)
	if err != nil {
		return err
	}
	a.dispatchPkgs = dispatchPkgs
	return nil
}
