package cli

// todo20-8/10：PlantUML 默认启动模式（headless）与尺寸上限提示。

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakePNG(w, h uint32) []byte {
	b := make([]byte, 24)
	copy(b, []byte("\x89PNG\r\n\x1a\n"))
	binary.BigEndian.PutUint32(b[8:12], 13)
	copy(b[12:16], "IHDR")
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	return b
}

func TestPlantumlJavaArgsHeadless(t *testing.T) {
	args := strings.Join(plantumlJavaArgs("/tmp/plantuml.jar"), " ")
	for _, want := range []string{"-Djava.awt.headless=true", "-Djava.io.tmpdir=", "-tpng", "-pipe"} {
		if !strings.Contains(args, want) {
			t.Errorf("java 启动参数应含 %q: %s", want, args)
		}
	}
}

func TestPlantumlSizeWarning(t *testing.T) {
	t.Setenv("PLANTUML_LIMIT_SIZE", "")
	if w := plantumlSizeWarning(fakePNG(500, 4096)); w == "" ||
		!strings.Contains(w, "尺寸上限 4096") || !strings.Contains(w, "PLANTUML_LIMIT_SIZE") {
		t.Errorf("触达默认上限应提示裁切与扩大方法: %q", w)
	}
	if w := plantumlSizeWarning(fakePNG(500, 4095)); w != "" {
		t.Errorf("未触达上限不应提示: %q", w)
	}
	if w := plantumlSizeWarning(fakePNG(4096, 500)); w == "" {
		t.Errorf("宽度触顶也应提示: %q", w)
	}

	t.Setenv("PLANTUML_LIMIT_SIZE", "16384")
	if w := plantumlSizeWarning(fakePNG(500, 4096)); w != "" {
		t.Errorf("显式放大上限后 4096 不再裁切: %q", w)
	}
	if w := plantumlSizeWarning(fakePNG(500, 16384)); w == "" ||
		!strings.Contains(w, "上限 16384") {
		t.Errorf("更大图达到新上限仍需提示: %q", w)
	}
	if w := plantumlSizeWarning([]byte("not a png")); w != "" {
		t.Errorf("非 PNG 不误报: %q", w)
	}
}

func TestPlantumlRenderTimeoutMessage(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "plantuml.jar")
	if err := os.WriteFile(jar, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	java := filepath.Join(dir, "java")
	if err := os.WriteFile(java, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PLANTUML_JAR", jar)
	t.Setenv("PLANTUML_TMPDIR", dir)
	oldCfg := agentConfigPath
	agentConfigPath = func() string { return filepath.Join(dir, "absent.yaml") }
	defer func() { agentConfigPath = oldCfg }()
	oldBudget := plantumlRenderBudget
	plantumlRenderBudget = 150 * time.Millisecond
	defer func() { plantumlRenderBudget = oldBudget }()

	_, err := plantumlRender("@startuml\n@enduml\n")
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if !strings.Contains(err.Error(), "渲染超时") || !strings.Contains(err.Error(), "150ms") {
		t.Errorf("超时错误应标注渲染超时与预算值: %v", err)
	}
	if strings.Contains(err.Error(), "signal: killed") && !strings.Contains(err.Error(), "渲染超时") {
		t.Errorf("不应只报 signal: killed: %v", err)
	}
}
