package action

// R94 迁移：`query external-interfaces` 查询逻辑（原
// cli/query_external_interfaces.go）——外部系统接口调用识别（R45 用户
// 要求）：本系统内有 grpc/http 接口调用，但：
// ① 被调接口在本项目内没有定义（grpc：目标服务无注册点/实现/方法
//    特征——外部调用创建的 grpc_service 只有 service_name；http：
//    目标路由无 handler 属性——外部 URL 创建的只有 path/method）
// ② 请求对象不在本项目服务的接口参数中（grpc：调用实参类型 ∉ 本
//    项目服务 param_types 集合；http 的 gin handler 无显式请求类型
//    ——http 只按条件①判定）
// 输出 JSON 契约：{interfaces: [{kind, service, method, req_type,
// callers: [{func, loc}]}]}。cli 只做参数转发与输出
// （cmdExternalInterfaces）；wiki 渲染经 Actions.ExternalInterfaces
// 同源调用。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ExtCaller 一个外部接口调用点。
type ExtCaller struct {
	Func string `json:"func"` // 调用函数短名
	Pkg  string `json:"pkg"`  // 调用方包路径（R47 架构图聚合——领域归属）
	Loc  string `json:"loc"`  // file:line
}

// ExternalInterface 一个外部接口（grpc 服务方法 / http URL）。
type ExternalInterface struct {
	Kind    string      `json:"kind"`     // grpc | http
	Service string      `json:"service"`  // grpc 服务名 / http host
	Method  string      `json:"method"`   // grpc 方法名 / http method+path
	ReqType string      `json:"req_type"` // 请求对象类型（grpc；http 空）
	Callers []ExtCaller `json:"callers"`  // 调用点
}

// ExternalInterfacesResult 查询结果契约。
type ExternalInterfacesResult struct {
	Interfaces []ExternalInterface `json:"interfaces"`
}

// ExternalInterfaces 识别外部接口调用：grpc_call/http_call 边 → 目标
// 节点特征判定（无本项目定义特征）+ grpc 请求对象比对。
func (a *Actions) ExternalInterfaces() (*ExternalInterfacesResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ExternalInterfaces")
	defer logger.Info("exit (Actions).ExternalInterfaces")
	res := &ExternalInterfacesResult{Interfaces: []ExternalInterface{}}
	// 1. 本项目 grpc 服务参数类型集合（param_types 属性——接口签名
	//    识别写入；逗号分隔完整路径）
	localParamTypes := map[string]bool{}
	svcs, err := a.repo.GetGrpcServices()
	if err != nil {
		return nil, err
	}
	for _, n := range svcs {
		for _, p := range strings.Split(n.Property("param_types"), ",") {
			if p = strings.TrimSpace(p); p != "" {
				localParamTypes[p] = true
			}
		}
	}

	// 2. grpc_call 边：目标服务无本项目定义特征 → 外部候选；请求实参
	//    类型 ∉ 本项目服务参数集合 → 确认
	grouped := map[string]*ExternalInterface{}
	addCaller := func(key string, ei *ExternalInterface, srcID string, line int) {
		if _, ok := grouped[key]; !ok {
			grouped[key] = ei
		}
		grouped[key].Callers = append(grouped[key].Callers, ExtCaller{
			Func: shortNameOf(srcID),
			Pkg:  pkgOfEntityID(srcID), // R47：调用方包路径（架构图领域聚合）
			Loc:  callerLoc(a.repo, srcID, line),
		})
	}
	grpcFacts, err := a.repo.GetFactsByKinds(string(domain.FactGrpcCall))
	if err != nil {
		return nil, err
	}
	for _, f := range grpcFacts {
		tgt := string(f.TargetID)
		// 目标服务是否本项目定义：有 registers_service/methods 属性
		if svcIsLocal(a.repo, tgt) {
			continue
		}
		method := metaStr(f.Metadata, "method")
		reqType := metaStr(f.Metadata, "req_type")
		// 条件②：请求对象在本项目服务参数中 → 排除（可能内部接口）
		if reqType != "" && localParamTypes[reqType] {
			continue
		}
		key := "grpc|" + tgt + "|" + method
		svcName := tgt
		if i := strings.LastIndex(tgt, ":svc."); i >= 0 {
			svcName = tgt[i+len(":svc."):]
		}
		addCaller(key, &ExternalInterface{
			Kind: "grpc", Service: svcName, Method: method, ReqType: reqType,
		}, string(f.SourceID), metaInt(f.Metadata, "line_num"))
	}

	// 3. http_call 边：目标路由无 handler 属性（外部 URL 创建的只有
	//    path/method——本项目路由带 handler/resolver/register）→ 外部
	//    R100 条件②：请求对象判定——调用请求体类型 ∉ 本项目路由请求
	//    类型集合（handler req_types）→ 确认外部；∈ → 排除（可能内部）
	localHTTPReqTypes := map[string]bool{}
	httpRoutes, err := a.repo.GetHTTPRouteNodes()
	if err != nil {
		return nil, err
	}
	for _, n := range httpRoutes {
		for _, p := range strings.Split(n.Property("req_types"), ",") {
			if p = strings.TrimSpace(p); p != "" {
				localHTTPReqTypes[p] = true
			}
		}
	}
	httpFacts, err := a.repo.GetFactsByKinds(string(domain.FactHTTPCall))
	if err != nil {
		return nil, err
	}
	for _, f := range httpFacts {
		tgt := string(f.TargetID)
		if routeIsLocal(a.repo, tgt) {
			continue
		}
		host := metaStr(f.Metadata, "host")
		method := metaStr(f.Metadata, "method")
		reqType := metaStr(f.Metadata, "req_type")
		if reqType != "" && localHTTPReqTypes[reqType] {
			continue
		}
		path := ""
		if i := strings.LastIndex(tgt, ":route."); i >= 0 {
			path = tgt[i+len(":route."):]
		}
		key := "http|" + tgt
		addCaller(key, &ExternalInterface{
			Kind: "http", Service: host, Method: strings.ToUpper(method) + " " + path, ReqType: reqType,
		}, string(f.SourceID), metaInt(f.Metadata, "line_num"))
	}

	// 4. 确定性排序（kind → service → method）
	for _, ei := range grouped {
		sort.Slice(ei.Callers, func(i, j int) bool {
			if ei.Callers[i].Func != ei.Callers[j].Func {
				return ei.Callers[i].Func < ei.Callers[j].Func
			}
			return ei.Callers[i].Loc < ei.Callers[j].Loc
		})
		res.Interfaces = append(res.Interfaces, *ei)
	}
	sort.Slice(res.Interfaces, func(i, j int) bool {
		if res.Interfaces[i].Kind != res.Interfaces[j].Kind {
			return res.Interfaces[i].Kind < res.Interfaces[j].Kind
		}
		if res.Interfaces[i].Service != res.Interfaces[j].Service {
			return res.Interfaces[i].Service < res.Interfaces[j].Service
		}
		return res.Interfaces[i].Method < res.Interfaces[j].Method
	})
	return res, nil
}

// svcIsLocal 目标 grpc 服务是否本项目定义（注册点 registers_service
// 或方法全集 methods 属性——外部调用创建的只有 service_name）。
func svcIsLocal(r Reader, svcID string) bool {
	n, err := r.GetSymbol(domain.CanonicalID(svcID))
	if err != nil || n == nil {
		return true // 查不到按本地处理（不误报）
	}
	return n.Property("registers_service") != "" || n.Property("methods") != ""
}

// routeIsLocal 目标 http 路由是否本项目定义（handler 属性——外部 URL
// 创建的只有 path/method）。
func routeIsLocal(r Reader, routeID string) bool {
	n, err := r.GetSymbol(domain.CanonicalID(routeID))
	if err != nil || n == nil {
		return true
	}
	return n.Property("handler") != ""
}

// callerLoc 调用函数位置（file:line——source 节点文件 + 调用行）。
func callerLoc(r Reader, srcID string, line int) string {
	n, err := r.GetSymbol(domain.CanonicalID(srcID))
	if err != nil || n == nil || n.FilePath == "" {
		return ""
	}
	if line > 0 {
		return fmt.Sprintf("%s:%d", n.FilePath, line)
	}
	return n.FilePath
}

// metaStr metadata 字符串值（scanFacts JSON 反序列化）。
func metaStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// metaInt metadata 数值（JSON 反序列化为 float64；int 兜底——测试
// 内联构造）。
func metaInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}
