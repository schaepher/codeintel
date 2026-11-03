package action

// go list 离线选包（外部依赖 / vendor），成功与失败都交给调用方缓存。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// seqGoListPkg `go list -e -json -find -mod=readonly <importPath>`（离线只读）。
// 成功返回 package Dir + 是否标准库；失败返回原因（查询阶段不构建索引、
// 不下载依赖）。
func seqGoListPkg(ctx context.Context, workDir, importPath string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-json", "-find", "-mod=readonly", importPath)
	cmd.Dir = workDir
	// 离线：禁止下载与 sumdb 访问；只读：不修改 go.mod/go.sum
	cmd.Env = append(seqFilteredEnv(os.Environ(), "GOFLAGS", "GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK", "GOPRIVATE"),
		"GOFLAGS=-mod=readonly", "GOPROXY=off", "GOSUMDB=off", "GONOSUMDB=*", "GONOSUMCHECK=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", false, &seqExternalError{Import: importPath, Err: err.Error(), Stderr: strings.TrimSpace(stderr.String())}
	}
	var out struct {
		Dir      string
		Standard bool
		Error    *struct{ Err string }
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return "", false, &seqExternalError{Import: importPath, Err: "解析 go list 输出失败: " + err.Error()}
	}
	if out.Error != nil && out.Error.Err != "" {
		return "", false, &seqExternalError{Import: importPath, Err: out.Error.Err}
	}
	if out.Dir == "" {
		return "", false, &seqExternalError{Import: importPath, Err: "go list 未返回包目录（外部依赖不可定位）"}
	}
	return out.Dir, out.Standard, nil
}

// seqExternalError 外部包定位失败（错误文本可读，缓存后不重复尝试）。
type seqExternalError struct {
	Import string
	Err    string
	Stderr string
}

func (e *seqExternalError) Error() string {
	msg := "外部包 " + e.Import + " 源码不可定位: " + e.Err
	if e.Stderr != "" {
		msg += "（go list: " + e.Stderr + "）"
	}
	return msg
}

// seqFilteredEnv 去掉指定环境变量后返回（避免与追加的离线值冲突）。
func seqFilteredEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		skip := false
		for _, k := range keys {
			if name == k {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	return out
}

// seqGoListVendorPkg vendor 模式离线选包：返回目录与选中源文件（GoFiles/
// CgoFiles）。20 秒上限，禁下载、禁改 module。
func seqGoListVendorPkg(moduleRoot, importPath string) (string, []string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-json", "-find", "-mod=vendor", importPath)
	cmd.Dir = moduleRoot
	cmd.Env = append(seqFilteredEnv(os.Environ(), "GOFLAGS", "GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK", "GOPRIVATE"),
		"GOFLAGS=-mod=vendor", "GOPROXY=off", "GOSUMDB=off", "GONOSUMDB=*", "GONOSUMCHECK=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", nil, fmt.Errorf("%v（%s）", err, strings.TrimSpace(stderr.String()))
	}
	var out struct {
		Dir      string
		GoFiles  []string
		CgoFiles []string
		Error    *struct{ Err string }
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return "", nil, fmt.Errorf("解析 go list 输出失败: %w", err)
	}
	if out.Error != nil && out.Error.Err != "" {
		return "", nil, fmt.Errorf("%s", out.Error.Err)
	}
	if out.Dir == "" {
		return "", nil, fmt.Errorf("go list 未返回 vendor 包目录")
	}
	files := append(append([]string{}, out.GoFiles...), out.CgoFiles...)
	return out.Dir, files, nil
}
