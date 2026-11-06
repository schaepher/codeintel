package cli

// sequence-batch manifest 参数预校验（从 sequence_batch.go 拆出）。

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
)

// seqBatchFlowInputs 预校验后的每流程输入（非法参数在执行前拒绝）。
type seqBatchFlowInputs struct {
	runtimePath string
	timeout     time.Duration
}

// seqBatchValidateFlows 统一校验 depth/continue/timeout/scene/selector/runtimeData。
func seqBatchValidateFlows(flows []seqBatchFlow, baseDir string) ([]seqBatchFlowInputs, error) {
	out := make([]seqBatchFlowInputs, len(flows))
	for i, fl := range flows {
		// B11c/B11e：非法 timeout 在打开索引/创建目录前拒绝（整串消费 +
		// 整数秒乘 time.Second 前检查范围）。
		if fl.Timeout != "" {
			dur, terr := parseBatchTimeout(fl.Timeout)
			if terr != nil || dur <= 0 {
				return nil, fmt.Errorf("flow %s timeout 非法 %q", fl.ID, fl.Timeout)
			}
			out[i].timeout = dur
		}
		if fl.Depth < 0 {
			return nil, fmt.Errorf("flow %s depth 非法 %d", fl.ID, fl.Depth)
		}
		if fl.ContinueSegments < 0 || fl.ContinueSegments > action.SeqContinuationMaxSegments {
			return nil, fmt.Errorf("flow %s continueSegments 非法 %d", fl.ID, fl.ContinueSegments)
		}
		for _, sc := range fl.Scene {
			k := strings.TrimSpace(sc)
			if k == "" || !strings.Contains(k, "=") {
				return nil, fmt.Errorf("flow %s scene 非法 %q（需要 k=v）", fl.ID, sc)
			}
			key := strings.TrimSpace(strings.SplitN(k, "=", 2)[0])
			if key == "" {
				return nil, fmt.Errorf("flow %s scene 非法 %q（键为空）", fl.ID, sc)
			}
		}
		if fl.CombinationSelector != "" && !seqBatchCanonicalFuncID(fl.CombinationSelector) {
			return nil, fmt.Errorf("flow %s combinationSelector 非法 %q（需要准确 canonical 函数 ID）",
				fl.ID, fl.CombinationSelector)
		}
		if fl.RuntimeData != "" {
			rp := fl.RuntimeData
			if !filepath.IsAbs(rp) {
				rp = filepath.Join(baseDir, rp)
			}
			abs, err := filepath.Abs(rp)
			if err != nil {
				return nil, fmt.Errorf("flow %s runtimeData 路径非法: %v", fl.ID, err)
			}
			rel, rerr := filepath.Rel(baseDir, abs)
			if rerr != nil || strings.HasPrefix(rel, "..") {
				return nil, fmt.Errorf("flow %s runtimeData 路径越界 %q", fl.ID, fl.RuntimeData)
			}
			if verr := action.ValidateRuntimeData(abs); verr != nil {
				return nil, fmt.Errorf("flow %s runtimeData 非法: %v", fl.ID, verr)
			}
			out[i].runtimePath = abs
		}
	}
	return out, nil
}

// seqBatchCanonicalFuncID 形如 symbol:go:<pkg>:<name>（至少 3 个非空段）。
func seqBatchCanonicalFuncID(id string) bool {
	if !strings.HasPrefix(id, "symbol:go:") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(id, "symbol:go:"), ":")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return false
		}
	}
	return true
}
