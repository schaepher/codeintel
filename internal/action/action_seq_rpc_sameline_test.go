package action

// todo25 §4.1：同一行两个服务同名 RPC 方法（旧索引无 pos 时的线号回退）
// 必须按静态方法名消歧；仍不唯一则未知，不取第一条。

import (
	"go/token"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

type rpcForwardFakeReader struct {
	Reader
	fwds []*domain.RPCForward
}

func (f *rpcForwardFakeReader) GetRPCForwards(domain.CanonicalID) ([]*domain.RPCForward, error) {
	return f.fwds, nil
}

func TestSeqRPCForwardSameLineDisambiguation(t *testing.T) {
	other := &domain.RPCForward{SourceID: "sym", HandlerName: "OtherService",
		Method: "Cancel", Line: 10, Protocol: domain.RPCProtocolMicro}
	account := &domain.RPCForward{SourceID: "sym", HandlerName: "AccountService",
		Method: "Apply", Line: 10, Protocol: domain.RPCProtocolMicro}
	actCancel := &domain.RPCForward{SourceID: "sym", HandlerName: "AccountService",
		Method: "Cancel", Line: 10, Protocol: domain.RPCProtocolMicro}
	// 故意把 OtherService 放在前面：不得"取第一个"。
	fake := &rpcForwardFakeReader{fwds: []*domain.RPCForward{other, actCancel, account}}
	acts := &Actions{repo: fake}
	req := CodeSequenceRequest{acc: &seqTimingAcc{}, rpcByFunc: map[string][]*domain.RPCForward{}}

	fwd, aggregated, ok := seqRPCForwardAt(acts, req, "sym",
		token.Position{Line: 10, Offset: 0}, "Apply")
	if !ok || fwd == nil || fwd.HandlerName != "AccountService" {
		t.Fatalf("同行同名方法应按方法名唯一匹配 AccountService，got %+v (ok=%v)", fwd, ok)
	}
	if !aggregated {
		t.Errorf("线号回退+方法名消歧应标为汇总命中（不是该位置直接 metadata）")
	}

	// 同行仍有两个同名方法（两个服务都 Apply）→ 不能猜。
	dup := &domain.RPCForward{SourceID: "sym", HandlerName: "ThirdService",
		Method: "Apply", Line: 10, Protocol: domain.RPCProtocolMicro}
	otherApply := &domain.RPCForward{SourceID: "sym", HandlerName: "OtherService",
		Method: "Apply", Line: 10, Protocol: domain.RPCProtocolMicro}
	fake.fwds = []*domain.RPCForward{otherApply, dup}
	req.rpcByFunc = map[string][]*domain.RPCForward{}
	if fwd, _, ok := seqRPCForwardAt(acts, req, "sym",
		token.Position{Line: 10}, "Apply"); ok || fwd != nil {
		t.Fatalf("同行多个同名方法应保持未知，got %+v", fwd)
	}

	// 方法名不匹配任何一条 → 也保持未知（不回退到第一条）。
	req.rpcByFunc = map[string][]*domain.RPCForward{}
	if fwd, _, ok := seqRPCForwardAt(acts, req, "sym",
		token.Position{Line: 10}, "Missing"); ok || fwd != nil {
		t.Fatalf("方法名不匹配不得借用同行其它 RPC，got %+v", fwd)
	}
}
