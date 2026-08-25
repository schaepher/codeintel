package cli

// R45 `codeintel query external-interfaces`——外部系统接口调用识别
// （用户要求）：本系统内有 grpc/http 接口调用，但：
// ① 被调接口在本项目内没有定义（grpc：目标服务无注册点/实现/方法
//    特征——外部调用创建的 grpc_service 只有 service_name；http：
//    目标路由无 handler 属性——外部 URL 创建的只有 path/method）
// ② 请求对象不在本项目服务的接口参数中（grpc：调用实参类型 ∉ 本
//    项目服务 param_types 集合；http 的 gin handler 无显式请求类型
//    ——http 只按条件①判定）
// 输出 JSON 契约：{interfaces: [{kind, service, method, req_type,
// callers: [{func, loc}]}]}。

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// extCaller 一个外部接口调用点。
type extCaller struct {
	Func string `json:"func"` // 调用函数短名
	Loc  string `json:"loc"`  // file:line
}

// externalInterface 一个外部接口（grpc 服务方法 / http URL）。
type externalInterface struct {
	Kind    string      `json:"kind"`     // grpc | http
	Service string      `json:"service"`  // grpc 服务名 / http host
	Method  string      `json:"method"`   // grpc 方法名 / http method+path
	ReqType string      `json:"req_type"` // 请求对象类型（grpc；http 空）
	Callers []extCaller `json:"callers"`  // 调用点
}

// externalInterfacesResult 查询结果契约。
type externalInterfacesResult struct {
	Interfaces []externalInterface `json:"interfaces"`
}

// externalInterfaces 识别外部接口调用：grpc_call/http_call 边 → 目标
// 节点特征判定（无本项目定义特征）+ grpc 请求对象比对。
func externalInterfaces(repo *sqlite.Repo) (*externalInterfacesResult, error) {
	res := &externalInterfacesResult{Interfaces: []externalInterface{}}
	// 1. 本项目 grpc 服务参数类型集合（param_types 属性——接口签名识别
	//    写入；逗号分隔完整路径）
	localParamTypes := map[string]bool{}
	rows, err := repo.Query(`SELECT COALESCE(json_extract(properties, '$.param_types'), '') FROM nodes WHERE kind = 'grpc_service'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err == nil && v != "" {
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					localParamTypes[p] = true
				}
			}
		}
	}
	rows.Close()

	// 2. grpc_call 边：目标服务无本项目定义特征 → 外部候选；请求实参
	//    类型 ∉ 本项目服务参数集合 → 确认
	grouped := map[string]*externalInterface{}
	addCaller := func(key string, ei *externalInterface, srcID string, line int) {
		if _, ok := grouped[key]; !ok {
			grouped[key] = ei
		}
		grouped[key].Callers = append(grouped[key].Callers, extCaller{
			Func: shortSymbolNameID(srcID),
			Loc:  callerLoc(repo, srcID, line),
		})
	}
	rows, err = repo.Query(`SELECT e.source_id, e.target_id,
		COALESCE(json_extract(e.metadata, '$.method'), ''),
		COALESCE(json_extract(e.metadata, '$.req_type'), ''),
		COALESCE(json_extract(e.metadata, '$.line_num'), 0)
		FROM edges e WHERE e.kind = 'grpc_call'`)
	if err != nil {
		return nil, err
	}
	var grpcEdges [][5]any
	for rows.Next() {
		var src, tgt, method, reqType string
		var line int
		if err := rows.Scan(&src, &tgt, &method, &reqType, &line); err != nil {
			continue
		}
		grpcEdges = append(grpcEdges, [5]any{src, tgt, method, reqType, line})
	}
	rows.Close()
	for _, e := range grpcEdges {
		tgt := e[1].(string)
		// 目标服务是否本项目定义：有 registers_service/methods 属性
		if svcIsLocal(repo, tgt) {
			continue
		}
		method := e[2].(string)
		reqType := e[3].(string)
		// 条件②：请求对象在本项目服务参数中 → 排除（可能内部接口）
		if reqType != "" && localParamTypes[reqType] {
			continue
		}
		key := "grpc|" + tgt + "|" + method
		svcName := tgt
		if i := strings.LastIndex(tgt, ":svc."); i >= 0 {
			svcName = tgt[i+len(":svc."):]
		}
		addCaller(key, &externalInterface{
			Kind: "grpc", Service: svcName, Method: method, ReqType: reqType,
		}, e[0].(string), e[4].(int))
	}

	// 3. http_call 边：目标路由无 handler 属性（外部 URL 创建的只有
	//    path/method——本项目路由带 handler/resolver/register）→ 外部
	rows, err = repo.Query(`SELECT e.source_id, e.target_id,
		COALESCE(json_extract(e.metadata, '$.url'), ''),
		COALESCE(json_extract(e.metadata, '$.host'), ''),
		COALESCE(json_extract(e.metadata, '$.method'), ''),
		COALESCE(json_extract(e.metadata, '$.line_num'), 0)
		FROM edges e WHERE e.kind = 'http_call'`)
	if err != nil {
		return nil, err
	}
	var httpEdges [][6]any
	for rows.Next() {
		var src, tgt, url, host, method string
		var line int
		if err := rows.Scan(&src, &tgt, &url, &host, &method, &line); err != nil {
			continue
		}
		httpEdges = append(httpEdges, [6]any{src, tgt, url, host, method, line})
	}
	rows.Close()
	for _, e := range httpEdges {
		tgt := e[1].(string)
		if routeIsLocal(repo, tgt) {
			continue
		}
		host := e[3].(string)
		method := e[4].(string)
		path := ""
		if i := strings.LastIndex(tgt, ":route."); i >= 0 {
			path = tgt[i+len(":route."):]
		}
		key := "http|" + tgt
		addCaller(key, &externalInterface{
			Kind: "http", Service: host, Method: strings.ToUpper(method) + " " + path,
		}, e[0].(string), e[5].(int))
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
func svcIsLocal(repo *sqlite.Repo, svcID string) bool {
	rows, err := repo.Query(`SELECT COALESCE(json_extract(properties, '$.registers_service'), ''),
		COALESCE(json_extract(properties, '$.methods'), '') FROM nodes WHERE id = ?`, svcID)
	if err != nil {
		return true // 查不到按本地处理（不误报）
	}
	defer rows.Close()
	if rows.Next() {
		var reg, methods string
		if err := rows.Scan(&reg, &methods); err == nil && (reg != "" || methods != "") {
			return true
		}
	}
	return false
}

// routeIsLocal 目标 http 路由是否本项目定义（handler 属性——外部 URL
// 创建的只有 path/method）。
func routeIsLocal(repo *sqlite.Repo, routeID string) bool {
	rows, err := repo.Query(`SELECT COALESCE(json_extract(properties, '$.handler'), '') FROM nodes WHERE id = ?`, routeID)
	if err != nil {
		return true
	}
	defer rows.Close()
	if rows.Next() {
		var h string
		if err := rows.Scan(&h); err == nil && h != "" {
			return true
		}
	}
	return false
}

// callerLoc 调用函数位置（file:line——source 节点文件 + 调用行）。
func callerLoc(repo *sqlite.Repo, srcID string, line int) string {
	rows, err := repo.Query(`SELECT file_path FROM nodes WHERE id = ?`, srcID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	if rows.Next() {
		var f string
		if err := rows.Scan(&f); err == nil && f != "" {
			if line > 0 {
				return fmt.Sprintf("%s:%d", f, line)
			}
			return f
		}
	}
	return ""
}

// cmdExternalInterfaces 实现 `codeintel query external-interfaces
// [--repo <path>] [--json]`——外部系统接口调用清单。
func cmdExternalInterfaces(repoAbs string, f queryFlags) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	res, err := externalInterfaces(sqlite.NewRepo(db))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.json {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	cur := ""
	for _, ei := range res.Interfaces {
		key := ei.Kind + "|" + ei.Service
		if key != cur {
			cur = key
			fmt.Printf("\n[%s] %s\n", ei.Kind, ei.Service)
		}
		fmt.Printf("  %s", ei.Method)
		if ei.ReqType != "" {
			fmt.Printf("（请求 %s）", ei.ReqType)
		}
		fmt.Printf(" ← %s\n", joinCallers(ei.Callers))
	}
	fmt.Printf("\n共 %d 个外部接口调用\n", len(res.Interfaces))
	return 0
}

// joinCallers / renderExternalInterfacesMD / renderExternalInterfacesHTML
// 已拆到 wiki_external.go（行数治理）。
