//go:build integration

package integration

// todo22 §9 夹具写入（模块文件 + 覆盖变体）。

import (
	"os"
	"path/filepath"
	"testing"
)

// flowMatrix 夹具路径。
type flowMatrix struct {
	dir string
}

// writeFlowMatrix 写夹具到临时目录（overrides 覆盖同名相对路径文件内容）。
func writeFlowMatrix(t *testing.T, overrides map[string]string) flowMatrix {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                     flowMatrixGoMod,
		"tx/go.mod":                  flowMatrixTxGoMod,
		"tx/tx.go":                   flowMatrixTxGo,
		"microstub/go.mod":           microFixtureStubGoMod,
		"microstub/client/client.go": microFixtureStubClient,
		"microstub/server/server.go": microFixtureStubServer,
		"microstub/micro.go":         microFixtureStubRoot,
		"pb/proto.go":                flowMatrixPb,
		"flow.go":                    flowMatrixCore,
		"flow_sync.go":               flowMatrixSync,
		"flow_review.go":             flowMatrixReviewForms,
		"flow_host.go":               flowMatrixHostForms,
	}
	for k, v := range overrides {
		files[k] = v
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, body)
	}
	return flowMatrix{dir: root}
}
