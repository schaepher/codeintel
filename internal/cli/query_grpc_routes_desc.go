package cli

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// serviceDescMethods go/parser 提取生成代码 ServiceDesc 方法全集：
// `var <Svc>_ServiceDesc = grpc.ServiceDesc{ Methods: []grpc.MethodDesc{
// {MethodName: "X", Handler: _X_Handler}, ... } }`。
func serviceDescMethods(pbFile, service string) []grpcRouteMethod {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, pbFile, nil, 0)
	if err != nil {
		return nil
	}
	want := service + "_ServiceDesc"
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != want {
				continue
			}
			lit, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				return nil
			}
			return methodDescs(lit)
		}
	}
	return nil
}

// methodDescs 从 ServiceDesc CompositeLit 提取 Methods 列表。
func methodDescs(desc *ast.CompositeLit) []grpcRouteMethod {
	for _, el := range desc.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Methods" {
			continue
		}
		methods, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			return nil
		}
		var out []grpcRouteMethod
		for _, m := range methods.Elts {
			ml, ok := m.(*ast.CompositeLit)
			if !ok {
				continue
			}
			var name, handler string
			for _, f := range ml.Elts {
				fkv, ok := f.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				fk, ok := fkv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				val := astExprString(fkv.Value)
				switch fk.Name {
				case "MethodName":
					name = val
				case "Handler":
					handler = val
				}
			}
			if name != "" {
				out = append(out, grpcRouteMethod{Name: name, Handler: handler})
			}
		}
		return out
	}
	return nil
}

// astExprString 表达式字符串值（字符串字面量/标识符名）。
func astExprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.BasicLit:
		if t.Kind == token.STRING {
			s, err := strconvUnquote(t.Value)
			if err == nil {
				return s
			}
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// cmdGrpcRoutes 实现 `codeintel query grpc-routes [--repo <path>] [--json]`
// ——服务端 gRPC 路由清单（契约化 JSON，Agent 直接解析）。
func cmdGrpcRoutes(repoAbs string, f queryFlags) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	res, err := grpcRoutes(sqlite.NewRepo(db), repoAbs)
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
	for _, s := range res.Services {
		fmt.Printf("[%s] 实现 %s（%s） 注册 %s\n", s.Name, s.Impl, s.ImplFile, s.Register)
		for _, m := range s.Methods {
			fmt.Printf("  %s（%s）\n", m.Name, m.Handler)
		}
	}
	fmt.Printf("\n共 %d 个 gRPC 服务\n", len(res.Services))
	return 0
}
