//go:build integration

package integration

// todo28 U07：条件工厂的调用参与者与图可用性（真实源码 → init → CLI）。
//
// receiver 工厂字段 / 单例工厂 switch 选实现时，子调用必须保留原调用
// Actor/Type（发送方非空且已声明），JSON 与 Mermaid 使用同一实际 receiver。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func u07Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/actors\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), `package actors
import "example.com/actors/region"
type API interface{Work()}
type One struct{}
func(*One)Work(){OneEvent()}
type Two struct{}
func(*Two)Work(){TwoEvent()}
func OneEvent(){}
func TwoEvent(){}
type Box struct{worker API}
func(b *Box)Set(v API){b.worker=v}
func(b *Box)Get()API{return b.worker}
func(b *Box)Run(){b.Step()}
func(b *Box)Step(){b.Get().Work()}
func Build(kind int)(*Box,error){
 box:=Box{}
 switch kind{case 1:box.Set(&One{});case 2:box.Set(&Two{});default:return nil,nil}
 return &box,nil
}
func Receiver(){box,err:=Build(1);_=err;box.Run()}
func Singleton(){region.Get().Work()}
`)
	writeFile(t, filepath.Join(dir, "region", "region.go"), `package region
import "os"
var selected API
type API interface{Work()}
type Handler struct{ID,MY interface{}}
type Base struct{}
func(*Base)Work(){BaseEvent()}
func BaseEvent(){}
type IDHandler struct{Base}
func(*IDHandler)Work(){IDEvent()}
func IDEvent(){}
type MYHandler struct{Base}
func Get()API{return selected}
func init(){initialize()}
func initialize(){selected=&Base{};selected=Choose(Handler{ID:&IDHandler{},MY:&MYHandler{}}).(API)}
func Choose(h Handler)interface{}{
 var v interface{}
 k:=region()
 switch k{case "ID":v=h.ID;case "MY":v=h.MY}
 if v==nil{panic("missing")}
 return v
}
func region()string{return os.Getenv("REGION")}
`)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// TestU07SingletonActor 单例工厂 switch 子调用保留发送方。
func TestU07SingletonActor(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u07Fixture(t)
	code, out := runCLIOut(t, "query", "sequence", "Singleton", "--repo", dir,
		"--code", "--depth", "12", "--continue-segments", "32", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	if strings.Contains(out, "  ->>") {
		t.Fatalf("消息发送方不得为空:\n%s", out)
	}
	if !strings.Contains(out, "alt factory symbol:go:example.com/actors/region:Choose: region() == \"ID\"") {
		t.Fatalf("缺少工厂 guard:\n%s", out)
	}
	if !strings.Contains(out, "id=symbol:go:example.com/actors/region:IDEvent verdict=direct") {
		t.Fatalf("缺少准确实现子调用:\n%s", out)
	}
	// 发送方必须是已声明参与者（P<n>），不能是空或未声明别名。
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "->>") {
			continue
		}
		left := strings.TrimSpace(strings.Split(strings.Split(line, "->>")[0], ":")[0])
		if left == "" || !strings.HasPrefix(left, "P") {
			t.Fatalf("消息发送方应为已声明参与者: %q", line)
		}
	}
}

// TestU07ReceiverActors receiver 工厂字段子调用（JSON/Mermaid 同一 receiver）。
func TestU07ReceiverActors(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u07Fixture(t)
	for _, args := range [][2]string{{"12", "32"}, {"1", "32"}} {
		code, out := runCLIOut(t, "query", "sequence", "Receiver", "--repo", dir,
			"--code", "--depth", args[0], "--continue-segments", args[1],
			"--evidence", "--json")
		if code != 0 {
			t.Fatalf("query exit=%d\n%s", code, out)
		}
		var res struct {
			Nodes []map[string]any `json:"Nodes"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("json: %v", err)
		}
		var empty int
		var implIDs []string
		var walk func(n map[string]any)
		walk = func(n map[string]any) {
			if n["Kind"] == "call" {
				if actor, _ := n["Actor"].(string); actor == "" {
					empty++
				}
				if ev, ok := n["evidence"].(map[string]any); ok {
					if id, _ := ev["symbol_id"].(string); strings.Contains(id, "One).Work") ||
						strings.Contains(id, "Two).Work") {
						implIDs = append(implIDs, id)
					}
				}
			}
			for _, key := range []string{"Nodes", "Else"} {
				if arr, ok := n[key].([]any); ok {
					for _, c := range arr {
						walk(c.(map[string]any))
					}
				}
			}
		}
		for _, n := range res.Nodes {
			walk(n)
		}
		if empty != 0 {
			t.Fatalf("depth=%s 有 %d 个调用参与者为空", args[0], empty)
		}
		if len(implIDs) == 0 {
			t.Fatalf("depth=%s 缺少准确实现 ID", args[0])
		}
	}
}
