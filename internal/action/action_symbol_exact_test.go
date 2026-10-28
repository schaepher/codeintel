package action

// todo24 §11.3：canonical ID 精确未命中禁止退回全库名称扫描。
//
// ResolveSymbol 收到 symbol:/file:/commit: 前缀的输入时，一律按准确 ID 查询；
// 未命中即返回可被 errors.Is(err, domain.ErrNotFound) 判断的错误——不进入
// GetSymbolByName（LIKE 全扫）、相似名池（AllSymbolNames）或全库扫描。
// 普通名称查询保留既有候选/歧义提示。

import (
	"errors"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// canonicalMissReader 记录并拒绝"名称扫描/全名池"类调用。
type canonicalMissReader struct {
	Reader
	allowNames  bool // 允许名称查询时透传（只用于"普通名称仍正常"的正例）
	nameLookups int
	allNames    int
}

func (r *canonicalMissReader) GetSymbolByName(name string) ([]*domain.CodeEntity, error) {
	r.nameLookups++
	if r.allowNames {
		return r.Reader.GetSymbolByName(name)
	}
	return nil, errors.New("禁止：canonical ID 未命中不得走名称扫描（§11.3）")
}

func (r *canonicalMissReader) AllSymbolNames(limit int) ([]string, error) {
	r.allNames++
	if r.allowNames {
		return r.Reader.AllSymbolNames(limit)
	}
	return nil, errors.New("禁止：不得拉全名池（§11.3）")
}

func TestResolveSymbolCanonicalMissNoNameScan(t *testing.T) {
	a, _ := seedRepo(t)
	orig := a.repo
	strict := &canonicalMissReader{Reader: orig}
	a.repo = strict

	for _, in := range []string{
		"symbol:go:example.com/m:Missing",
		"file:missing.go",
		"commit:missing",
	} {
		if _, err := a.ResolveSymbol(in); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s 未命中应返回 ErrNotFound，got %v", in, err)
		}
	}
	if strict.nameLookups != 0 || strict.allNames != 0 {
		t.Errorf("准确 ID 未命中不得名称扫描：GetSymbolByName=%d AllSymbolNames=%d",
			strict.nameLookups, strict.allNames)
	}

	// 普通名称查询继续走名称查询（候选/歧义提示保留）。
	allowed := &canonicalMissReader{Reader: orig, allowNames: true}
	a.repo = allowed
	n, err := a.ResolveSymbol("main")
	if err != nil || n.Name != "main" {
		t.Fatalf("名称查询应正常: %v, %v", n, err)
	}
	if allowed.nameLookups == 0 {
		t.Error("普通名称查询应调用 GetSymbolByName")
	}
}

// TestResolveSymbolCanonicalMissWithManyNodes：大量无关节点不改变"名称查询
// 与相似名池调用次数均为零"。
func TestResolveSymbolCanonicalMissWithManyNodes(t *testing.T) {
	a, dir := seedRepo(t)
	noise := make([]*domain.CodeEntity, 0, 200)
	for i := 0; i < 200; i++ {
		id := domain.CanonicalID("symbol:go:example.com/m:noise" + string(rune('A'+i%26)) + string(rune('0'+i/26)))
		noise = append(noise, &domain.CodeEntity{ID: id, Kind: domain.KindFunction,
			Name: string(id), FilePath: "noise.go"})
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := sqlite.NewRepo(db).SaveBatchStats(noise, nil, nil); err != nil {
		t.Fatal(err)
	}
	strict := &canonicalMissReader{Reader: a.repo}
	a.repo = strict
	if _, err := a.ResolveSymbol("symbol:go:example.com/m:Missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("未命中应返回 ErrNotFound，got %v", err)
	}
	if strict.nameLookups != 0 || strict.allNames != 0 {
		t.Errorf("无关节点不得改变计数：GetSymbolByName=%d AllSymbolNames=%d",
			strict.nameLookups, strict.allNames)
	}
}
