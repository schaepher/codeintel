package action

// todo21-14.1：内部 canonical ID 的签名查询必须精确，且已取消的查询不接触
// 仓储。原实现 sigTypesOf → ResolveSymbol，精确 miss 后退化为
// GetSymbolByName 的 LIKE 全扫 + AllSymbolNames 候选池（逐调用点重复）。

import (
	"context"
	"errors"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// signatureLookupReader 只实现本测试用到的仓储方法（其余经嵌入的 nil
// Reader 满足接口——被调用即 panic，防止"偷偷查库"）。
type signatureLookupReader struct {
	Reader
	node  *domain.CodeEntity
	fuzzy int
}

func TestSequenceAlreadyCanceledDoesNotQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(nil).CodeSequence(CodeSequenceRequest{Ctx: ctx}); !errors.Is(err, ErrSeqCanceled) {
		t.Fatalf("expected canceled without touching storage: %v", err)
	}
}

func (r *signatureLookupReader) GetSymbol(domain.CanonicalID) (*domain.CodeEntity, error) {
	if r.node == nil {
		return nil, domain.ErrNotFound
	}
	return r.node, nil
}

func (r *signatureLookupReader) GetSymbolByName(string) ([]*domain.CodeEntity, error) {
	r.fuzzy++
	return nil, nil
}

func (r *signatureLookupReader) AllSymbolNames(int) ([]string, error) {
	r.fuzzy++
	return nil, nil
}

func TestSequenceSignatureLookupIsExact(t *testing.T) {
	r := &signatureLookupReader{}
	a := New(r)
	if _, _, ok := sigTypesOf(a, "symbol:go:example.com/external:(Client).Call"); ok {
		t.Fatal("missing signature reported as known")
	}
	if r.fuzzy != 0 {
		t.Fatalf("internal canonical ID triggered %d fuzzy searches", r.fuzzy)
	}
	r.node = &domain.CodeEntity{Properties: map[string]any{
		"signature": "func (Client).Call(req Request) (Reply, error)",
	}}
	args, rets, ok := sigTypesOf(a, "symbol:go:example.com/external:(Client).Call")
	if !ok || len(args) != 1 || args[0] != "Request" || len(rets) != 2 || rets[0] != "Reply" {
		t.Fatalf("signature lost: %v %v %v", args, rets, ok)
	}
}
