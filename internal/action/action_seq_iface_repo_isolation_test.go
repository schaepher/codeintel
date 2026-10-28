package action

// todo23 §7.1 补充：外层 workspace 请求复用同一份请求缓存时的仓库隔离检验
// （同名接口 / Missing 方法不得跨仓串用）。

import (
	"strings"
	"testing"
)

// TestSeqIfaceCacheRepoIsolation：外层 workspace 请求复用同一份请求缓存时，
// 同名接口（相同 canonical ID）在两个仓库的实现候选不得互相串用；Missing
// 也不得借用 Insert 的结果。
func TestSeqIfaceCacheRepoIsolation(t *testing.T) {
	actsA, dirA := seedIfaceRepoWithImpl(t, "StoreA")
	actsB, dirB := seedIfaceRepoWithImpl(t, "StoreB")
	req := CodeSequenceRequest{RepoAbs: dirA, ifaceImpls: map[string]seqIfaceCached{},
		ifaceImplTypes: map[string][]string{}}

	insertA, candsA, ok := actsA.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).Insert")
	if !ok || insertA != "symbol:go:example.com/api:(StoreA).Insert" {
		t.Fatalf("仓库 A 的 Insert 应具体化到 StoreA.Insert，got %q %v", insertA, candsA)
	}
	if len(candsA) != 1 || !strings.HasSuffix(candsA[0], "(StoreA).Insert") {
		t.Errorf("仓库 A 候选不得含其它仓库实现：%v", candsA)
	}
	saveA, candsAS, ok := actsA.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).SaveService")
	if !ok || saveA != "symbol:go:example.com/api:(StoreA).SaveService" {
		t.Errorf("仓库 A 的 SaveService 应具体化到 StoreA.SaveService，got %q %v", saveA, candsAS)
	}

	req.RepoAbs = dirB
	insertB, candsB, ok := actsB.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).Insert")
	if !ok || insertB != "symbol:go:example.com/api:(StoreB).Insert" {
		t.Fatalf("仓库 B 的 Insert 应具体化到 StoreB.Insert（不得命中 A 的缓存），got %q %v", insertB, candsB)
	}
	if len(candsB) != 1 || !strings.HasSuffix(candsB[0], "(StoreB).Insert") {
		t.Errorf("仓库 B 候选不得含 A 的实现：%v", candsB)
	}
	missing, candsM, okM := actsB.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).Missing")
	if okM || missing != "" || len(candsM) != 0 {
		t.Errorf("Missing 不得借用 Insert/SaveService 或其它仓库的结果，got %q %v (%v)", missing, candsM, okM)
	}

	// 冷暖对照：同一仓库第二次查询（缓存命中）结论逐项相同。
	again, candsAgain, okAgain := actsB.seqIfaceMethodImpl(req, "symbol:go:example.com/api:(API).Insert")
	if !okAgain || again != insertB || strings.Join(candsAgain, "|") != strings.Join(candsB, "|") {
		t.Errorf("暖查询结论应与冷查询一致：cold=%q %v warm=%q %v", insertB, candsB, again, candsAgain)
	}
}
