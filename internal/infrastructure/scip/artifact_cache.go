package scip

// P1-08：SCIP 产物内容缓存（全量构建用）。
//
// 背景（go2o 实测，2026-10-27）：暖构建（SSA 包缓存 127/127 命中）仍有
// 2.7–4.4s 花在 SCIP 适配器——其中 scip-go 子进程本身 1.2s，其余是 32MB
// protobuf 解码与节点/边发射（含 reflect/protobuf 分配）。包缓存已经按
// 内容跳过 SSA 分析，SCIP 侧没有对应机制 → 每次全量重建都重新跑一遍
// 外部索引器。
//
// 这里缓存"一条构建的 SCIP 发射产物"（节点/边，保持原发射顺序），键 =
// 分析范围内容指纹（与 SSA 包缓存同一口径，含模块内包 + 依赖包源码 hash）
// + scip-go 版本 + 模块目录集合 + 缓存格式版本。命中则跳过子进程与
// protobuf 解码，直接重放。
//
// 失效与边界：
//   - 源码/依赖内容变化 → 内容指纹变 → 键变（旧缓存自然失效）
//   - scip-go 升级 → 版本进键
//   - 适配器逻辑变化 → scipCacheFormat 递增（与 pkgCacheFormat 同纪律）
//   - 增量构建（按变更包传 package-patterns）**不用缓存**：分析范围不同，
//     且增量本身只索引变更包（P1-12），无需全量重放
//   - 编码/解码失败 → 视为未命中（构建继续，仅少一次加速），不静默改变
//     产物语义（失败时不写半份缓存）

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// scipCacheFormat 缓存文件格式版本（结构/语义变更时递增，旧缓存失效）。
const scipCacheFormat = 1

// artifactChunkSize 单条 gob 消息的条目数（同包缓存分块纪律：限制编码
// 缓冲峰值，避免整份产物进一个消息）。
//
// 缓存文件是**固定路径**（scip-artifacts.gob），键存在文件头里：源码每变
// 一次键就变，若按 key 命名会在 .codeintel/cache 下无限累积（一份 3MB+）；
// 固定路径 + 头校验 = 最多留一份，失配时直接整体覆盖。
const artifactChunkSize = 200

// artifactHeader 缓存文件头（先读头，键不符直接拒绝，不做大载荷解码）。
type artifactHeader struct {
	Version     int
	Format      string // "scip-artifacts"
	Key         string
	ScipVersion string
}

// artifactItem 一个发射条目（节点或边；保持原顺序）。
type artifactItem struct {
	Node *domain.CodeEntity
	Fact *domain.Fact
}

// artifactChunk 分块载荷：Items 为条目，End=true 表示流结束（最后一条）。
type artifactChunk struct {
	Items []artifactItem
	End   bool
}

// scipCacheKey 组合缓存键：格式 + 内容指纹 + scip-go 版本 + 模块目录。
func scipCacheKey(contentKey, scipVersion string, moduleRels []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "scip-artifacts-v%d\x00", scipCacheFormat)
	h.Write([]byte(contentKey))
	h.Write([]byte{0})
	h.Write([]byte(scipVersion))
	for _, rel := range moduleRels {
		h.Write([]byte{0})
		h.Write([]byte(rel))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// scipCachePath 缓存文件路径（固定名；键在文件头里校验）。
func scipCachePath(repoPath string) string {
	return filepath.Join(repoPath, ".codeintel", "cache", "scip-artifacts.gob")
}

// loadArtifacts 读取缓存产物；文件缺失/损坏/键不符/缺结束标记 → (nil,false)。
func loadArtifacts(path, key, scipVersion string) ([]artifactItem, bool) {
	logger := zap.L()
	logger.Debug("enter loadArtifacts")
	defer logger.Debug("exit loadArtifacts")
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	dec := gob.NewDecoder(f)
	var head artifactHeader
	if err := dec.Decode(&head); err != nil {
		return nil, false
	}
	if head.Version != scipCacheFormat || head.Format != "scip-artifacts" ||
		head.Key != key || head.ScipVersion != scipVersion {
		return nil, false
	}
	var out []artifactItem
	for {
		var chunk artifactChunk
		if err := dec.Decode(&chunk); err != nil {
			return nil, false // 截断/类型不符：结束标记前出错 → 拒绝
		}
		if chunk.End {
			if len(chunk.Items) != 0 {
				return nil, false // 结束标记不应携带载荷
			}
			return out, true
		}
		out = append(out, chunk.Items...)
	}
}

// saveArtifacts 写缓存产物（先写临时文件再 rename，失败不留半份）。
// 编码失败（例如属性里出现 gob 不支持的类型）→ 删除临时文件并返回错误，
// 调用方只记日志，不改变构建结果。
func saveArtifacts(path, key, scipVersion string, items []artifactItem) error {
	logger := zap.L()
	logger.Debug("enter saveArtifacts")
	defer logger.Debug("exit saveArtifacts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := gob.NewEncoder(f)
	werr := enc.Encode(artifactHeader{Version: scipCacheFormat, Format: "scip-artifacts",
		Key: key, ScipVersion: scipVersion})
	for start := 0; start < len(items) && werr == nil; start += artifactChunkSize {
		end := start + artifactChunkSize
		if end > len(items) {
			end = len(items)
		}
		werr = enc.Encode(artifactChunk{Items: items[start:end]})
	}
	if werr == nil {
		werr = enc.Encode(artifactChunk{End: true})
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	return os.Rename(tmp, path)
}
