package action

// T06：DI 字段注入契约——`/*@Inject(name=worker)*/` + 准确注册
// （RegisterBeanByType/reflect.TypeOf）+ 容器自身 GetBean/FieldByName/Set 证明；
// 契约缺失/动态 key/多重注册/非 init 注册保持未知（不借唯一实现）。

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

const t06FlowSrc = `package flow

import (
	"reflect"

	"example.com/m/api"
	"example.com/m/di"
)

type Server struct {
	/*@Inject(name=worker)*/ Worker api.API
}

func (s *Server) Run() { s.Worker.Work() }

func init() { di.Container.RegisterBeanByType("server", reflect.TypeOf((*Server)(nil))) }
`

const t06APISrc = `package api

import (
	"reflect"

	"example.com/m/di"
)

type API interface{ Work() }
type Worker struct{}
type Other struct{}

func (*Worker) Work() { Hit() }
func (*Other) Work()  { Miss() }
func Hit()            {}
func Miss()           {}

func init() { di.Container.RegisterBeanByType("worker", reflect.TypeOf((*Worker)(nil))) }
`

// t06DISrc 容器实现：注解读取 + GetBean + FieldByName + Set（注入机制契约）。
const t06DISrc = `package di

import (
	"reflect"
	"strings"
	"sync"
)

type container struct{ aopMap sync.Map }

var Container container

func (*container) RegisterBeanByType(string, reflect.Type) {}
func (*container) GetBean(string) interface{}              { return nil }

type anno struct{}

func (*anno) Name() string                   { return "" }
func (*anno) Value() map[string]string       { return nil }

type annotations struct{}

func (annotations) GetStructByAnno(string, map[string]string) []*anno { return nil }

var annotation struct{ AnnoContainer annotations }
var a, injectAnno *anno
var fieldName string

func wire(bean interface{}, beanName string) {
	annos := annotation.AnnoContainer.GetStructByAnno("Bean", map[string]string{"name": beanName})
	_ = annos
	if a.Name() == "Inject" {
		injectAnno = a
	}
	b := injectAnno.Value()["name"]
	if !strings.HasPrefix(beanName, "aop_") {
		if !strings.HasSuffix(beanName, "_"+b) {
			bn, _ := Container.aopMap.Load(b)
			if bn != nil {
				b = bn.(string)
			}
		}
	}
	injectBean := Container.GetBean(b)
	it := reflect.ValueOf(bean).Elem()
	val := it.FieldByName(fieldName)
	v := reflect.ValueOf(injectBean)
	val.Set(v)
}
`

// t06Seed 建立三包夹具（flow/api/di）+ init 函数节点；返回 Actions 与仓库根。
// t06Line 源码中包含 substr 的行号。
func t06Line(t *testing.T, src, substr string) int {
	t.Helper()
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, substr) {
			return i + 1
		}
	}
	t.Fatalf("fixture 找不到 %q", substr)
	return 0
}

func t06Seed(t *testing.T, flowSrc, apiSrc, diSrc string) (*Actions, string) {
	t.Helper()
	files := map[string]string{
		"go.mod":     "module example.com/m\n\ngo 1.26.2\n",
		"flow.go":    flowSrc,
		"api/api.go": apiSrc,
		"di/di.go":   diSrc,
	}
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Server", Kind: domain.KindStruct, Name: "Server"},
		{ID: "symbol:go:example.com/m:(Server).Run", Kind: domain.KindMethod,
			Name: "(Server).Run", FilePath: "flow.go", LineStart: t06Line(t, flowSrc, "func (s *Server) Run()")},
		{ID: "symbol:go:example.com/m:init", Kind: domain.KindFunction, Name: "init",
			FilePath: "flow.go", LineStart: t06Line(t, flowSrc, "func init()")},
		{ID: "symbol:go:example.com/m/api:API", Kind: domain.KindInterface, Name: "API"},
		{ID: "symbol:go:example.com/m/api:(API).Work", Kind: domain.KindMethod, Name: "(API).Work"},
		{ID: "symbol:go:example.com/m/api:Worker", Kind: domain.KindStruct, Name: "Worker"},
		{ID: "symbol:go:example.com/m/api:(Worker).Work", Kind: domain.KindMethod,
			Name: "(Worker).Work", FilePath: "api/api.go",
			LineStart: t06Line(t, apiSrc, "func (*Worker) Work()")},
		{ID: "symbol:go:example.com/m/api:Hit", Kind: domain.KindFunction, Name: "Hit",
			FilePath: "api/api.go", LineStart: t06Line(t, apiSrc, "func Hit()")},
		{ID: "symbol:go:example.com/m/di:wire", Kind: domain.KindFunction, Name: "wire",
			FilePath: "di/di.go", LineStart: t06Line(t, diSrc, "func wire(")},
	}
	if strings.Contains(apiSrc, "func init()") {
		nodes = append(nodes, &domain.CodeEntity{ID: "symbol:go:example.com/m/api:init",
			Kind: domain.KindFunction, Name: "init", FilePath: "api/api.go",
			LineStart: t06Line(t, apiSrc, "func init()")})
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:(Server).Run",
			TargetID: "symbol:go:example.com/m/api:(API).Work",
			Kind:     domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": t06Line(t, flowSrc, "s.Worker.Work()")}},
		{SourceID: "symbol:go:example.com/m/api:(Worker).Work",
			TargetID: "symbol:go:example.com/m/api:Hit",
			Kind:     domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": t06Line(t, apiSrc, "func (*Worker) Work()")}},
		{SourceID: "symbol:go:example.com/m/api:API",
			TargetID: "symbol:go:example.com/m/api:Worker", Kind: domain.FactImplements, Confidence: 1.0},
	}
	acts, dir := seedT04(t, files, nodes, facts)
	return acts, dir
}

func t06Run(t *testing.T, acts *Actions, dir string) *CodeSeqNode {
	t.Helper()
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(Server).Run", RepoAbs: dir, Depth: 5})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestT06DIInjectionContract(t *testing.T) {
	acts, dir := t06Seed(t, t06FlowSrc, t06APISrc, t06DISrc)
	root := t06Run(t, acts, dir)
	call := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && strings.HasSuffix(n.Label, ".Worker.Work")
	})
	if len(call) != 1 {
		t.Fatalf("应有 1 个字段注入调用: %v", labelsOf(root.Nodes))
	}
	ev := call[0].Evidence
	if ev == nil || ev.SymbolID != "symbol:go:example.com/m/api:(Worker).Work" {
		t.Fatalf("证据应指向准确实现: %+v", ev)
	}
	if ev.Source != SeqEvidenceDI || ev.Reason != seqReasonDIRegistration {
		t.Errorf("证据来源/依据 = %s/%s", ev.Source, ev.Reason)
	}
	if ev.Verdict != SeqVerdictCorroborated {
		t.Errorf("verdict = %q, want corroborated", ev.Verdict)
	}
	// 注册点 + 容器注入代码都有位置（可核对）
	if ev.RegisterFile == "" || ev.RegisterSite == 0 {
		t.Errorf("缺注册点证据: %+v", ev)
	}
	if ev.DIInjectFile == "" || ev.DIInjectLine == 0 {
		t.Errorf("缺容器注入代码证据: %+v", ev)
	}
	if len(seqCallNodesNamed(root, "Hit")) != 1 {
		t.Errorf("实现内部调用应展开（Hit=1）: %v", labelsOf(root.Nodes))
	}
}

// 容器没有 GetBean/FieldByName/Set 证明 → 即使注册/类型/唯一实现都在也未知。
func TestT06ContainerContractMissing(t *testing.T) {
	di := strings.Replace(t06DISrc, "\tval.Set(v)\n", "", 1)
	acts, dir := t06Seed(t, t06FlowSrc, t06APISrc, di)
	root := t06Run(t, acts, dir)
	if got := len(seqCallNodesNamed(root, "Hit")); got != 0 {
		t.Fatalf("容器契约缺失不得接成确定调用（Hit=%d）", got)
	}
	for _, n := range seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && strings.HasSuffix(n.Label, ".Worker.Work")
	}) {
		if n.Stop == nil || !strings.Contains(n.Stop.Detail, "注入代码") {
			t.Errorf("应明确容器注入代码未证明: %+v", n.Stop)
		}
	}
}

// 动态 key 注册 → 不算证据（保持未知）。
func TestT06DynamicKeyRegistration(t *testing.T) {
	api := strings.Replace(t06APISrc,
		`di.Container.RegisterBeanByType("worker", reflect.TypeOf((*Worker)(nil)))`,
		`di.Container.RegisterBeanByType(di.InjectName, reflect.TypeOf((*Worker)(nil)))`, 1)
	acts, dir := t06Seed(t, t06FlowSrc, api, t06DISrc)
	root := t06Run(t, acts, dir)
	if got := len(seqCallNodesNamed(root, "Hit")); got != 0 {
		t.Fatalf("动态 key 不得作为证据（Hit=%d）", got)
	}
}

// 非 init 注册（普通函数里）不证明执行 → 不算证据。
func TestT06NonInitRegistration(t *testing.T) {
	api := strings.Replace(t06APISrc,
		`func init() { di.Container.RegisterBeanByType("worker", reflect.TypeOf((*Worker)(nil))) }`,
		`func setup() { di.Container.RegisterBeanByType("worker", reflect.TypeOf((*Worker)(nil))) }`, 1)
	acts, dir := t06Seed(t, t06FlowSrc, api, t06DISrc)
	root := t06Run(t, acts, dir)
	if got := len(seqCallNodesNamed(root, "Hit")); got != 0 {
		t.Fatalf("非 init 注册不应作为证据（Hit=%d）", got)
	}
}
