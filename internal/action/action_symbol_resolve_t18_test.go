package action

// T18：准确缺失的 canonical ID 解析不得退回模糊名称搜索/相似名池（回归）。

import (
	"errors"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// strictSymbolReader 记录并拒绝模糊名称类调用（canonical 精确查询除外）。
type strictSymbolReader struct {
	Reader
	nameLookups int
	allNames    int
}

func (s *strictSymbolReader) GetSymbolByName(name string) ([]*domain.CodeEntity, error) {
	s.nameLookups++
	return nil, errors.New("禁止：canonical ID 缺失不得走 LIKE 名称扫描")
}

func (s *strictSymbolReader) AllSymbolNames(limit int) ([]string, error) {
	s.allNames++
	return nil, errors.New("禁止：canonical ID 缺失不得拉相似名池")
}

func TestResolveSymbolCanonicalMissNoFuzzySearch(t *testing.T) {
	acts, _ := seedRepo(t)
	strict := &strictSymbolReader{Reader: acts.repo}
	acts.repo = strict

	for _, id := range []string{
		"symbol:go:example.com/m:NoSuchSymbol",
		"file:not/there.go",
		"commit:deadbeef",
	} {
		n, err := acts.ResolveSymbol(id)
		if err == nil || n != nil {
			t.Errorf("%s：精确查询未命中应返回错误，got n=%+v err=%v", id, n, err)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s：错误应包裹 ErrNotFound，got %v", id, err)
		}
		if !strings.Contains(err.Error(), "精确查询未命中") {
			t.Errorf("%s：错误应说明精确查询口径: %v", id, err)
		}
	}
	if strict.nameLookups != 0 || strict.allNames != 0 {
		t.Errorf("canonical 缺失不得走模糊搜索：GetSymbolByName=%d AllSymbolNames=%d",
			strict.nameLookups, strict.allNames)
	}

	// 普通名称查询仍保留候选/相似名提示路径（不误伤）。
	if _, err := acts.ResolveSymbol("NoSuchPlainName"); err == nil {
		t.Error("普通名称未命中应返回错误")
	}
	if strict.nameLookups == 0 {
		t.Error("普通名称应走名称查询（不得因 canonical 规则一起禁掉）")
	}
}
