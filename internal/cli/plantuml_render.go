package cli

// R32 plantuml 渲染：java -jar plantuml.jar -tpng -pipe（stdin 输入、
// stdout PNG 字节）。jar 路径：PLANTUML_JAR env > /opt/plantuml/plantuml.jar。
// **java tmpdir 必须仓库外**（/tmp EDQUOT 教训——ImageIO 用
// java.io.tmpdir 写 PNG 缓存，配额满直接 I/O error；-Djava.io.tmpdir
// 指到仓库外 .tmp-build/gotmp）。渲染失败由调用方降级为文本块。

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// plantumlJarPath 定位 plantuml.jar（全局配置 plantuml_jar >
// PLANTUML_JAR > /opt/plantuml/plantuml.jar——用户：配置了就用配置，
// 没配置就尝试直接执行默认路径）。
func plantumlJarPath() string {
	if p := plantumlJarFromConfig(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
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

// plantumlJarFromConfig 读全局配置 plantuml_jar（~/.codeintel/config.yaml）。
func plantumlJarFromConfig() string {
	p := agentConfigPath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var c struct {
		PlantumlJar string `yaml:"plantuml_jar"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return ""
	}
	return c.PlantumlJar
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
//
// todo20-8：命令行场景一律显式 headless（不依赖调用方的 JAVA_TOOL_OPTIONS）
// ——AWT 初始化在无交互环境会挂起，此前默认路径 30s 超时被杀。
// todo20-10：尺寸上限不静默——PNG 维度触顶由调用方 plantumlSizeWarning
// 提示可能裁切（本函数只负责渲染，成功 ≠ 完整图）。
// plantumlRenderBudget 渲染预算（可测试注入；生产固定 30s——headless
// 修复后正常渲染 <2s，超时只用于真正挂起的 java）。
var plantumlRenderBudget = 30 * time.Second

// plantumlDefaultLimit PlantUML 未设置 PLANTUML_LIMIT_SIZE 时的标准上限。
const plantumlDefaultLimit = 4096

// plantumlJavaArgs java 启动参数（todo20-8：命令行场景固定 headless，
// 不依赖调用方的 JAVA_TOOL_OPTIONS；tmpdir 在仓库外）。
func plantumlJavaArgs(jar string) []string {
	return []string{
		"-Djava.awt.headless=true",
		"-Djava.io.tmpdir=" + plantumlTmpDir(), "-jar", jar, "-tpng", "-pipe",
	}
}

func plantumlRender(puml string) ([]byte, error) {
	jar := plantumlJarPath()
	if jar == "" {
		return nil, fmt.Errorf("未找到 plantuml.jar（设 PLANTUML_JAR 或装到 /opt/plantuml/）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), plantumlRenderBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "java", plantumlJavaArgs(jar)...)
	cmd.Stdin = strings.NewReader(puml)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		// todo20-8：超时必须与语法错误/其他进程错误区分（不再只报 signal: killed）。
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("plantuml 渲染超时（预算 %s，java 进程已终止）；"+
				"这是渲染器预算而非图语法错误——请确认 java/plantuml 可独立运行，或拆分过大的图\n%s",
				plantumlRenderBudget, errb.String())
		}
		return nil, fmt.Errorf("plantuml 渲染失败: %v\n%s", err, errb.String())
	}
	if out.Len() == 0 {
		return nil, fmt.Errorf("plantuml 输出为空")
	}
	return out.Bytes(), nil
}

// pngDimensions 读 PNG IHDR 宽高（非 PNG 返回 ok=false）。
func pngDimensions(b []byte) (w, h int, ok bool) {
	if len(b) < 24 || !bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n")) || string(b[12:16]) != "IHDR" {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24])), true
}

// plantumlSizeLimit 当前进程环境下 PlantUML 的尺寸上限（默认 4096）。
func plantumlSizeLimit() int {
	if s := os.Getenv("PLANTUML_LIMIT_SIZE"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return plantumlDefaultLimit
}

// plantumlSizeWarning PNG 任一维达到 PlantUML 尺寸上限时返回可操作提示
// （todo20-10：成功出口不得把"生成了 PNG"等同于"完整图"）。
func plantumlSizeWarning(png []byte) string {
	w, h, ok := pngDimensions(png)
	if !ok {
		return ""
	}
	limit := plantumlSizeLimit()
	if w < limit && h < limit {
		return ""
	}
	hint := "可设置 PLANTUML_LIMIT_SIZE=16384（或更大）后重新生成"
	if jar := plantumlJarPath(); jar != "" {
		hint += fmt.Sprintf("；或用 `java -Djava.awt.headless=true -jar %s -tsvg <结果.puml>` 输出完整矢量图", jar)
	}
	return fmt.Sprintf("PNG %dx%d 已达到 PlantUML 尺寸上限 %d，图片可能被裁切（底部内容缺失）；%s",
		w, h, limit, hint)
}
