//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// u34Actions 前端动作 → {name: action}。
func u34Actions(t *testing.T, dir, frontend string) map[string]map[string]any {
	t.Helper()
	root := u34Query(t, dir, "SubmitHTTP", "--related-entry",
		"symbol:go:example.com/remaining:ReviewHTTP", "--frontend-root", frontend)
	wf, _ := root["workflow"].(map[string]any)
	fr, _ := wf["frontend"].(map[string]any)
	out := map[string]map[string]any{}
	for _, raw := range asAnySlice(fr["actions"]) {
		a, _ := raw.(map[string]any)
		out[asString(a["action"])] = a
	}
	return out
}

// B09e.1 默认导出声明：默认导入解析到准确函数与请求。
func TestFrontendDefaultDeclarations(t *testing.T) {
	dir := u34Repo(t)
	a := u34Actions(t, dir, "default-frontend")["send"]
	if a == nil || asString(a["method"]) != "POST" || asString(a["path"]) != "/requests" ||
		asString(a["entry"]) != "symbol:go:example.com/remaining:SubmitHTTP" {
		t.Fatalf("default action = %v", a)
	}
	body, _ := a["request_body"].(map[string]any)
	id, _ := body["id"].(map[string]any)
	if asString(id["kind"]) != "parameter" || id["parameter"] != float64(0) {
		t.Fatalf("default request_body = %v（want id.parameter=0）", body)
	}
	// 默认值重导出：`export {default} from './a'` 解析到准确声明。
	re := u34Actions(t, dir, "reexport-frontend")["send"]
	if re == nil || asString(re["path"]) != "/requests" ||
		asString(re["entry"]) != "symbol:go:example.com/remaining:SubmitHTTP" {
		t.Fatalf("re-export action = %v（want /requests → SubmitHTTP）", re)
	}
	// 反例：缺源码 / 非函数默认导出 → 保留动作与 unknown，不连接旧目标。
	for _, frontend := range []string{"missing-frontend", "object-frontend"} {
		a := u34Actions(t, dir, frontend)["send"]
		if a == nil || asString(a["unknown"]) == "" || asString(a["entry"]) != "" {
			t.Fatalf("%s action = %v（want 保留动作 + unknown，无 entry）", frontend, a)
		}
	}
}

// B09e.2 文件与导出身份：同名函数按导入文件解析，冲突默认导出不猜。
func TestFrontendExportIdentity(t *testing.T) {
	dir := u34Repo(t)
	act := u34Actions(t, dir, "alias-frontend")
	for _, name := range []string{"click", "submitClick"} {
		a := act[name]
		if a == nil || asString(a["path"]) != "/requests" ||
			asString(a["entry"]) != "symbol:go:example.com/remaining:SubmitHTTP" {
			t.Fatalf("%s action = %v", name, a)
		}
	}
	same := u34Actions(t, dir, "same-name")["send"]
	if same == nil || asString(same["path"]) != "/alternate" ||
		asString(same["entry"]) != "symbol:go:example.com/remaining:SubmitHTTP" {
		t.Fatalf("same-name action = %v（want /alternate：按导入文件 a.ts）", same)
	}
	conflict := u34Actions(t, dir, "conflict-frontend")["send"]
	if conflict == nil || asString(conflict["unknown"]) == "" || asString(conflict["path"]) != "" {
		t.Fatalf("conflict default action = %v（want unknown、无路径）", conflict)
	}
}

// B09e.3 动态及预算：动态 URL 保留动作 + unknown，不伪造目标。
func TestFrontendUnresolvedActions(t *testing.T) {
	dir := u34Repo(t)
	a := u34Actions(t, dir, "dynamic-frontend")["send"]
	if a == nil {
		t.Fatal("动态 URL 动作被静默丢弃")
	}
	if asString(a["unknown"]) == "" || asString(a["entry"]) != "" {
		t.Fatalf("动态 URL action = %v（want unknown、无 entry）", a)
	}
}

// B09e.4 解构与字段：请求体字段按声明/实参来源；未知 body 不借用旧值。
func TestFrontendBodyFieldIdentity(t *testing.T) {
	dir := u34Repo(t)
	a := u34Actions(t, dir, "alias-frontend")["click"]
	body, _ := a["request_body"].(map[string]any)
	id, _ := body["id"].(map[string]any)
	if asString(id["kind"]) != "parameter" || id["parameter"] != float64(0) {
		t.Fatalf("positive request_body = %v", body)
	}
	// 反例：body 不是对象字面量（字段被覆写/来自另一对象）→ 不借用形参来源。
	b := u34Actions(t, dir, "body-frontend")["send"]
	if b == nil {
		t.Fatal("body-frontend 动作被丢弃")
	}
	if rb, _ := b["request_body"].(map[string]any); len(rb) != 0 {
		t.Fatalf("negative request_body = %v（want 空：来源未知）", rb)
	}
}

// u34Plantuml 取 Markdown 里的 PlantUML 图块。
func u34Plantuml(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	start := strings.Index(s, "```plantuml\n")
	if start < 0 {
		t.Fatalf("%s 缺少 PlantUML 图块", path)
	}
	rest := s[start+len("```plantuml\n"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// u34Export 执行一次 sequence-batch 导出并返回输出目录。
func u34Export(t *testing.T, dir, manifest, name string) (string, string) {
	t.Helper()
	mpath := filepath.Join(dir, name+".json")
	writeFile(t, mpath, manifest)
	outDir := filepath.Join(filepath.Dir(dir), name+"-export")
	if code, out := runCLIOut(t, "sequence-batch", "--manifest", mpath, "--out", outDir,
		"--json"); code != 0 {
		t.Fatalf("%s batch exit=%d\n%s", name, code, out)
	}
	return outDir, mpath
}

// B10e.1 图中实际动作：前端动作参与者、请求消息与后端 handler 同图。
func TestWorkflowFrontendDiagram(t *testing.T) {
	dir := u34Repo(t)
	manifest := `{"version":1,"flows":[{"id":"web","repo":".","entry":"symbol:go:example.com/remaining:SubmitHTTP","relatedEntries":["symbol:go:example.com/remaining:ReviewHTTP"],"frontendRoots":["alias-frontend"]}],"formats":["markdown","svg"]}`
	outDir, _ := u34Export(t, dir, manifest, "u34diagram")
	puml := u34Plantuml(t, filepath.Join(outDir, "web.md"))
	for _, want := range []string{"click", "submitClick", "SubmitHTTP", "POST /requests"} {
		if !strings.Contains(puml, want) {
			t.Fatalf("图中缺少 %q：\n%s", want, puml)
		}
	}
	// 图块真实渲染：SVG 与 Markdown 使用同一动作事实。
	svg, err := os.ReadFile(filepath.Join(outDir, "web.svg"))
	if err != nil || len(svg) == 0 {
		t.Fatalf("SVG 渲染失败: %v", err)
	}
	for _, want := range []string{"click", "submitClick", "SubmitHTTP"} {
		if !strings.Contains(string(svg), want) {
			t.Fatalf("SVG 缺少 %q（与 Markdown 不一致）", want)
		}
	}
	// 反例：缺前端源码 → 明确说明，不伪造动作参与者。
	manifest = `{"version":1,"flows":[{"id":"web","repo":".","entry":"symbol:go:example.com/remaining:SubmitHTTP","relatedEntries":["symbol:go:example.com/remaining:ReviewHTTP"],"frontendRoots":["missing-frontend"]}],"formats":["markdown"]}`
	outDir, _ = u34Export(t, dir, manifest, "u34diagram-missing")
	neg := u34Plantuml(t, filepath.Join(outDir, "web.md"))
	if strings.Contains(neg, "participant \"send\"") || !strings.Contains(neg, "目标未知") {
		t.Fatalf("缺源码时不得伪造动作参与者，必须说明未知：\n%s", neg)
	}
}

// B10e.2 同一流程事实：JSON 与组合 Markdown 使用同一动作/路径/参数。
func TestWorkflowOutputConsistency(t *testing.T) {
	dir := u34Repo(t)
	// 用同名函数夹具改变路径，所有出口必须一起变化。
	manifest := `{"version":1,"flows":[{"id":"web","repo":".","entry":"symbol:go:example.com/remaining:SubmitHTTP","relatedEntries":["symbol:go:example.com/remaining:ReviewHTTP"],"frontendRoots":["same-name"]}],"formats":["markdown"]}`
	outDir, _ := u34Export(t, dir, manifest, "u34consistent")
	puml := u34Plantuml(t, filepath.Join(outDir, "web.md"))
	if !strings.Contains(puml, "POST /alternate") || strings.Contains(puml, "POST /requests") {
		t.Fatalf("图与 JSON 动作不一致：\n%s", puml)
	}
	root := u34Query(t, dir, "SubmitHTTP", "--related-entry",
		"symbol:go:example.com/remaining:ReviewHTTP", "--frontend-root", "same-name")
	wf, _ := root["workflow"].(map[string]any)
	fr, _ := wf["frontend"].(map[string]any)
	for _, raw := range asAnySlice(fr["actions"]) {
		a, _ := raw.(map[string]any)
		if asString(a["path"]) != "/alternate" {
			t.Fatalf("JSON 动作路径 = %v（want /alternate）", a)
		}
	}
}

// B10e.4 失败隔离：运行期单项失败不阻止其它项导出。
func TestWorkflowExportFailures(t *testing.T) {
	dir := u34Repo(t)
	manifest := `{"version":1,"flows":[{"id":"good","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","relatedEntries":["symbol:go:example.com/remaining:Review"]},{"id":"bad","repo":".","entry":"symbol:go:example.com/remaining:NoSuchEntry"}],"formats":["markdown"]}`
	mpath := filepath.Join(dir, "u34fail.json")
	writeFile(t, mpath, manifest)
	outDir := filepath.Join(filepath.Dir(dir), "u34fail-export")
	code, out := runCLIOut(t, "sequence-batch", "--manifest", mpath, "--out", outDir, "--json")
	if code != 1 {
		t.Fatalf("单项失败应非零退出: code=%d\n%s", code, out)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("batch JSON: %v\n%s", err, out)
	}
	if summary["exported"] != float64(1) || summary["failed"] != float64(1) {
		t.Fatalf("summary = %v（want exported=1 failed=1）", summary)
	}
	if _, err := os.Stat(filepath.Join(outDir, "good.md")); err != nil {
		t.Fatalf("成功项未导出: %v", err)
	}
}

// B11e.1/B11e.2 timeout 范围：上限内原值生效；溢出/零/负值/非法后缀拒绝。
func TestTimeoutIntegerBounds(t *testing.T) {
	dir := u34Repo(t)
	base := filepath.Dir(dir)
	maxSeconds := strconv.FormatInt(seqTimeoutMaxSecondsForTest(), 10)
	// 正例：1 秒与 floor(MaxInt64/time.Second) 秒都必须被接受（原值生效）。
	for i, raw := range []string{"1", maxSeconds, "30s", "1h30m"} {
		manifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","relatedEntries":["symbol:go:example.com/remaining:Review"],"timeout":"` + raw + `"}],"formats":["markdown"]}`
		mpath := filepath.Join(dir, "u34ok-"+strconv.Itoa(i)+".json")
		writeFile(t, mpath, manifest)
		outDir := filepath.Join(base, "u34ok-"+strconv.Itoa(i))
		if code, out := runCLIOut(t, "sequence-batch", "--manifest", mpath, "--out", outDir, "--json"); code != 0 {
			t.Fatalf("合法 timeout %q 失败: %d\n%s", raw, code, out)
		}
	}
	// 反例：上限加 1、溢出、零、负值、超 int64、非法后缀。
	nextMax, _ := strconv.ParseInt(maxSeconds, 10, 64)
	bad := []string{strconv.FormatInt(nextMax+1, 10), "20000000000", "0", "-5",
		"999999999999999999999999", "3600oops", "0s", "-1s", "9223372036854775808ns"}
	for i, raw := range bad {
		manifest := `{"version":1,"flows":[{"id":"review","repo":".","entry":"symbol:go:example.com/remaining:SubmitTx","relatedEntries":["symbol:go:example.com/remaining:Review"],"timeout":"` + raw + `"}],"formats":["markdown"]}`
		mpath := filepath.Join(dir, "u34bad-"+strconv.Itoa(i)+".json")
		writeFile(t, mpath, manifest)
		outDir := filepath.Join(base, "u34bad-"+strconv.Itoa(i))
		code, out := runCLIOut(t, "sequence-batch", "--manifest", mpath, "--out", outDir, "--json")
		if code != 1 || strings.TrimSpace(out) != "" {
			t.Fatalf("timeout %q: code=%d out=%q（want 1/空）", raw, code, out)
		}
		if _, err := os.Stat(outDir); !os.IsNotExist(err) {
			t.Fatalf("timeout %q 不应创建输出目录", raw)
		}
	}
}

// seqTimeoutMaxSecondsForTest floor(MaxInt64 / time.Second)（与解析器同一口径）。
func seqTimeoutMaxSecondsForTest() int64 {
	return int64(^uint64(0)>>1) / int64(1000000000)
}
