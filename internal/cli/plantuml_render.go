package cli

// R32 plantuml 渲染：java -jar plantuml.jar -tpng -pipe（stdin 输入、
// stdout PNG 字节）。jar 路径：PLANTUML_JAR env > /opt/plantuml/plantuml.jar。
// **java tmpdir 必须仓库外**（/tmp EDQUOT 教训——ImageIO 用
// java.io.tmpdir 写 PNG 缓存，配额满直接 I/O error；-Djava.io.tmpdir
// 指到仓库外 .tmp-build/gotmp）。渲染失败由调用方降级为文本块。

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// plantumlJarPath 定位 plantuml.jar（PLANTUML_JAR > /opt/plantuml/plantuml.jar）。
func plantumlJarPath() string {
	if p := os.Getenv("PLANTUML_JAR"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if _, err := os.Stat("/opt/plantuml/plantuml.jar"); err == nil {
		return "/opt/plantuml/plantuml.jar"
	}
	return ""
}

// plantumlTmpDir java 临时目录（仓库外——/tmp 配额满）。
func plantumlTmpDir() string {
	if p := os.Getenv("PLANTUML_TMPDIR"); p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		d := filepath.Join(home, ".tmp-build", "gotmp")
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	return os.TempDir()
}

// plantumlRender puml 文本 → PNG 字节（30s 超时；无 jar/失败返回错误）。
func plantumlRender(puml string) ([]byte, error) {
	jar := plantumlJarPath()
	if jar == "" {
		return nil, fmt.Errorf("未找到 plantuml.jar（设 PLANTUML_JAR 或装到 /opt/plantuml/）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "java",
		"-Djava.io.tmpdir="+plantumlTmpDir(), "-jar", jar, "-tpng", "-pipe")
	cmd.Stdin = strings.NewReader(puml)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("plantuml 渲染失败: %v\n%s", err, errb.String())
	}
	if out.Len() == 0 {
		return nil, fmt.Errorf("plantuml 输出为空")
	}
	return out.Bytes(), nil
}
