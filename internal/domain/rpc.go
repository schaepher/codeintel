package domain

// RPC 传输协议标识（grpc_call 边的服务节点属性 rpc_protocol）。
const (
	RPCProtocolGrpc  = "grpc"  // native gRPC（google.golang.org/grpc / protoc 生成代码）
	RPCProtocolMicro = "micro" // go-micro（client.NewRequest/Call + server.Handle 注册）
)

// RPCForward 客户端 RPC 调用转发（todo22 §4.1）。
//
// 一条 grpc_call 出边表示"某函数是 RPC 客户端调用点"：
//   - native gRPC：SourceID 是业务调用方函数（AST 直接识别 XxxClient.Method）；
//   - go-micro：SourceID 是生成客户端的桩方法（NewRequest/Call 在桩内），
//     服务端续接由桩方法的 grpc_call 出边恢复。
//
// ServiceName 是服务短名（grpc_service 节点 service_name 属性，与既有
// grpc-routes/时序一致的命名口径）；FullIdentity 是 go-micro 的完整服务
// 身份（NewRequest 第一个实参，节点属性 rpc_service）——两者都保留，
// 避免"优化"丢掉非标准服务名。
type RPCForward struct {
	SourceID     CanonicalID `json:"source_id"`
	ServiceID    CanonicalID `json:"service_id"`
	ServiceName  string      `json:"service_name"`
	FullIdentity string      `json:"full_identity,omitempty"` // 客户端声明的完整身份
	// RegistrationIdentity 服务端注册的完整身份（micro.Name）——与客户端声明
	// 不同即身份冲突（改名/配错），调用方必须 fail-closed，不能"按短名兜底续接"。
	RegistrationIdentity string `json:"registration_identity,omitempty"`
	HandlerName          string `json:"handler_name,omitempty"` // 注册函数第二参接口名（去 Handler 后缀）
	Protocol             string `json:"protocol"`               // grpc | micro
	Method               string `json:"method"`
	Line                 int    `json:"line,omitempty"`
	Pos                  int    `json:"pos,omitempty"` // 调用点字节偏移（同行多调用可区分）
}
