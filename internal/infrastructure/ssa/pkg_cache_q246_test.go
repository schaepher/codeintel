package ssa

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q246：包产物收集器改为「按块序号收集、最后一块到达即落盘并释放」——
// 原实现把全量产物留到构建尾部统一写（go2o 峰值堆近 1GB 的来源）。
// 本测试锁定三个不变量：① 块乱序完成也按块序号拼接（确定性）；② 整包
// 最后一块到达后从集合中释放；③ 缓存键为空时不写文件。

func TestPkgCollectSetAssemblesInBlockOrder(t *testing.T) {
	dir := t.TempDir()
	const pkgPath = "example.com/mtest/app"
	set := newPkgCollectSet(dir, map[string]int{pkgPath: 3}, map[string]string{pkgPath: "hash-1"})

	node := func(id string) *domain.CodeEntity {
		return &domain.CodeEntity{ID: domain.CanonicalID(id), Kind: domain.KindFunction, Name: id}
	}
	fact := func(id string) *domain.Fact {
		return &domain.Fact{SourceID: domain.CanonicalID(id), TargetID: domain.CanonicalID(id + "-t"),
			Kind: domain.FactCalls, ToolSource: domain.ToolSSA, Confidence: 0.8}
	}
	// 模拟块乱序完成：2 → 0 → 1
	put := func(idx int) bool {
		pc := set.get(pkgPath)
		pc.blocks[idx] = []*domain.CodeEntity{node("n" + string(rune('0'+idx)))}
		pc.facts[idx] = []*domain.Fact{fact("f" + string(rune('0'+idx)))}
		pc.fd[idx] = map[domain.CanonicalID]*funcData{
			domain.CanonicalID("fd" + string(rune('0'+idx))): {calls: []callInfo{{calleeID: "c"}}},
		}
		pc.done++
		ready := pc.done == pc.total
		if ready {
			set.release(pkgPath)
			pc.save() // 与 adapter_index 的调用顺序一致：释放后（锁外）落盘
		}
		return ready
	}
	if put(2) || put(0) {
		t.Fatal("前两块完成时不应就绪")
	}
	if !put(1) {
		t.Fatal("最后一块完成应就绪")
	}
	if _, ok := set.cols[pkgPath]; ok {
		t.Error("整包写完后应从集合释放（内存不驻留全量产物）")
	}

	got := loadPkgCache(pkgCachePath(dir, pkgPath), "hash-1")
	if got == nil {
		t.Fatal("缓存应已落盘且 hash 匹配可命中")
	}
	if len(got.Nodes) != 3 || len(got.Facts) != 3 || len(got.FuncData) != 3 {
		t.Fatalf("产物应完整（3 节点/3 边/3 funcData），got %d/%d/%d",
			len(got.Nodes), len(got.Facts), len(got.FuncData))
	}
	for i := 0; i < 3; i++ {
		if want := "n" + string(rune('0'+i)); string(got.Nodes[i].ID) != want {
			t.Errorf("节点顺序应按块序号（确定性），[%d]=%s want %s", i, got.Nodes[i].ID, want)
		}
		if want := "f" + string(rune('0'+i)); string(got.Facts[i].SourceID) != want {
			t.Errorf("边顺序应按块序号，[%d]=%s want %s", i, got.Facts[i].SourceID, want)
		}
	}
	for i := 0; i < 3; i++ {
		id := "fd" + string(rune('0'+i))
		if _, ok := got.FuncData[id]; !ok {
			t.Errorf("funcData %s 未合并进缓存", id)
		}
	}
}

// TestPkgCollectSetEmptyHashNoFile：缓存键为空（源码 hash 计算失败）时
// 不写文件——与旧实现 `if hash != ""` 语义一致。
func TestPkgCollectSetEmptyHashNoFile(t *testing.T) {
	dir := t.TempDir()
	const pkgPath = "example.com/mtest/nohash"
	set := newPkgCollectSet(dir, map[string]int{pkgPath: 1}, map[string]string{pkgPath: ""})
	pc := set.get(pkgPath)
	pc.blocks[0] = []*domain.CodeEntity{{ID: "symbol:go:mtest:f", Kind: domain.KindFunction, Name: "f"}}
	pc.done++
	pc.save()
	if _, err := os.Stat(pkgCachePath(dir, pkgPath)); !os.IsNotExist(err) {
		t.Errorf("hash 为空不应写缓存文件，stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".codeintel", "cache")); !os.IsNotExist(err) {
		t.Errorf("hash 为空不应建 cache 目录，stat err=%v", err)
	}
}
