//go:build integration

package integration

// todo27 T06：DI 容器身份与注入代码契约（真实 init → CLI）。
//
// 覆盖：准确容器 + key + 变量级注入链（annotation→GetBean→ValueOf→
// FieldByName→Set）→ 准确实现；错误容器 / 无关联 Set / 动态 key / 条件
// 注册 / 非引导函数注册 / 错误 key → 不升级为确定实现。

import (
	"path/filepath"
	"strings"
	"testing"
)

const t06FlowSrc = `package flow
import("reflect";"example.com/flow/di";"example.com/flow/api")
type /*@Bean(name=server)*/ Server struct{
 /*@Inject(name=worker)*/ Worker api.API
}
func(s *Server)Run(){s.Worker.Work()}
func init(){di.Container.RegisterBeanByType("server",reflect.TypeOf((*Server)(nil)))}
`

const t06APISrc = `package api
import("reflect";"example.com/flow/di")
type API interface{Work()}
type Worker struct{}
type Other struct{}
func(*Worker)Work(){Hit()}
func(*Other)Work(){Miss()}
func Hit(){}
func Miss(){}
func init(){di.Container.RegisterBeanByType("worker",reflect.TypeOf((*Worker)(nil)))}
`

const t06DISrc = `package di
import("reflect";"strings";"sync")
type container struct{aopMap sync.Map}
var Container container
var OtherContainer container
func(*container)RegisterBeanByType(string,reflect.Type){}
func(*container)GetBean(string)interface{}{return nil}
type anno struct{}
func(*anno)Name()string{return ""}
func(*anno)Value()map[string]string{return nil}
type annotations struct{}
func(annotations)GetStructByAnno(string,map[string]string)[]*anno{return nil}
var annotation struct{AnnoContainer annotations}
var a,injectAnno *anno
var fieldName string
func wire(bean interface{},beanName string){
 annos:=annotation.AnnoContainer.GetStructByAnno("Bean",map[string]string{"name":beanName})
 _=annos
 if a.Name()=="Inject"{injectAnno=a}
 b:=injectAnno.Value()["name"]
 if !strings.HasPrefix(beanName,"aop_"){
  if !strings.HasSuffix(beanName,"_"+b){bn,_:=Container.aopMap.Load(b);if bn!=nil{b=bn.(string)}}
 }
 injectBean:=Container.GetBean(b)
 it:=reflect.ValueOf(bean).Elem()
 val:=it.FieldByName(fieldName)
 v:=reflect.ValueOf(injectBean)
 val.Set(v)
}
`

// t06Variant 生成变体：只改 api 注册/容器注入或 di 容器名。
func t06Variant(t *testing.T, apiSrc, diSrc string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t06FlowSrc)
	writeFile(t, filepath.Join(dir, "api", "api.go"), apiSrc)
	writeFile(t, filepath.Join(dir, "di", "di.go"), diSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func t06RunSeq(t *testing.T, dir, entry string) (string, string) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	code2, jsonOut := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--format", "json")
	if code2 != 0 {
		t.Fatalf("query json exit=%d\n%s", code2, jsonOut)
	}
	return out, jsonOut
}

// TestT06BaseContract：E-T06 正例——准确容器/key/注入链 → Worker.Work → Hit。
func TestT06BaseContract(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t06Variant(t, t06APISrc, t06DISrc)
	out, _ := t06RunSeq(t, dir, "(Server).Run")
	if !strings.Contains(out, "id=symbol:go:example.com/flow/api:(Worker).Work verdict=corroborated") {
		t.Errorf("DI 正例应具体化到 Worker.Work（di-field-registration）\n%s", out)
	}
	if !strings.Contains(out, "id=symbol:go:example.com/flow/api:Hit verdict=") {
		t.Errorf("应展开 Worker.Work 内部 Hit\n%s", out)
	}
}

// TestT06RejectVariants：错误容器/无关联 Set/动态 key/条件注册/非引导注册/
// 错误 key 都不得升级为确定实现。
func TestT06RejectVariants(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	// 错误容器：注册在 Container，注入代码用 OtherContainer。
	diWrongContainer := strings.Replace(t06DISrc, "injectBean:=Container.GetBean(b)",
		"injectBean:=OtherContainer.GetBean(b)", 1)
	// 无关联赋值：Set 的值不来自 GetBean（契约链断）。
	diUnrelated := strings.Replace(t06DISrc, "val.Set(v)",
		"val.Set(reflect.ValueOf(bean));_=v", 1)
	// 动态 key：注册 key 来自变量（不是字面量）。
	apiDynamicKey := strings.Replace(t06APISrc,
		`func init(){di.Container.RegisterBeanByType("worker",reflect.TypeOf((*Worker)(nil)))}`,
		`func init(){k:=beanKey();di.Container.RegisterBeanByType(k,reflect.TypeOf((*Worker)(nil)))}
func beanKey()string{return "worker"}`, 1)
	// 条件注册：无法证明执行。
	apiCond := strings.Replace(t06APISrc,
		`func init(){di.Container.RegisterBeanByType("worker",reflect.TypeOf((*Worker)(nil)))}`,
		`var flag = true
func init(){if flag{di.Container.RegisterBeanByType("worker",reflect.TypeOf((*Worker)(nil)))}}`, 1)
	// 非引导函数注册：不在 init/Init/Setup/Bootstrap 里。
	apiNonInit := strings.Replace(t06APISrc,
		`func init(){di.Container.RegisterBeanByType("worker",reflect.TypeOf((*Worker)(nil)))}`,
		`func registerDeps(){di.Container.RegisterBeanByType("worker",reflect.TypeOf((*Worker)(nil)))}`, 1)
	// 错误 key：注解 worker，注册成 other。
	apiWrongKey := strings.Replace(t06APISrc, `RegisterBeanByType("worker"`, `RegisterBeanByType("other"`, 1)

	cases := []struct{ name, api, di string }{
		{"wrong-container", t06APISrc, diWrongContainer},
		{"unrelated-set", t06APISrc, diUnrelated},
		{"dynamic-key", apiDynamicKey, t06DISrc},
		{"conditional-register", apiCond, t06DISrc},
		{"non-init-register", apiNonInit, t06DISrc},
		{"wrong-key", apiWrongKey, t06DISrc},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t06Variant(t, c.api, c.di)
			out, _ := t06RunSeq(t, dir, "(Server).Run")
			if strings.Contains(out, "id=symbol:go:example.com/flow/api:(Worker).Work verdict=corroborated") {
				t.Errorf("%s：不应把注册候选当确定实现\n%s", c.name, out)
			}
			if !strings.Contains(out, "unknown_receiver") && !strings.Contains(out, "iface_ambiguous") &&
				!strings.Contains(out, "unknown_impl") && !strings.Contains(out, "无写入/注入证明") &&
				!strings.Contains(out, "func_value_unbound") {
				t.Errorf("%s：应给出明确未证明诊断\n%s", c.name, out)
			}
		})
	}
}
