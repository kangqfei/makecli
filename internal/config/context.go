/**
 * [INPUT]: 无外部依赖
 * [OUTPUT]: 对外提供 Context 类型、DefaultContext 常量、LookupContext / ContextNames 函数
 * [POS]: internal/config 的后端拓扑中枢，把 dev/test/production 三套后端 URL 收成一等 context preset，作 cmd 层解析链的兜底层。
 *        词汇约定：context = 连哪套 Make 后端（dev/test/production）；environment 一词只属于 app 的部署环境（beta/production），两者不混用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

// ---------------------------------- context preset ----------------------------------

// Context 是一个后端 context 的 URL preset——把"永远一起出现"的数据泥团收编为对象。
// 均为主机基址（scheme://host），不含路径：
//   - MetaServerURL / RepoServerURL 的网关前缀 /api/make 由 cmd 层 withGateway 统一补齐
//   - AuthServerURL 为身份服务器基址，login 追加 .well-known 路径
//   - AgentGatewayURL 为 Agent 平台 gateway 基址，daemon 直连（无网关前缀）
//   - TraceServerURL 为 OpenObserve 基址，trace 子命令拼 /web/traces/trace-details 直达页
type Context struct {
	MetaServerURL   string
	RepoServerURL   string
	AuthServerURL   string
	AgentGatewayURL string
	TraceServerURL  string
}

// DefaultContext 是未配置 [settings] context 时的默认 context（生产已上线，默认收口到 production）。
const DefaultContext = "production"

// contexts 是内建 context preset 表：dev/test 用 qtech.cn（{dev-,test-} 前缀），production 用 qfei.cn。
// OpenObserve 无 dev/test 之分：两个非生产 context 共用 openobserve.qtech.cn。
var contexts = map[string]Context{
	"dev": {
		MetaServerURL:   "https://dev-make.qtech.cn",
		RepoServerURL:   "https://dev-make-repo.qtech.cn",
		AuthServerURL:   "https://dev-myaccount.qtech.cn",
		AgentGatewayURL: "https://dev-make-agent.qtech.cn",
		TraceServerURL:  "https://openobserve.qtech.cn",
	},
	"test": {
		MetaServerURL:   "https://test-make.qtech.cn",
		RepoServerURL:   "https://test-make-repo.qtech.cn",
		AuthServerURL:   "https://test-myaccount.qtech.cn",
		AgentGatewayURL: "https://test-make-agent.qtech.cn",
		TraceServerURL:  "https://openobserve.qtech.cn",
	},
	"production": {
		MetaServerURL:   "https://make.qfei.cn",
		RepoServerURL:   "https://make-repo.qfei.cn",
		AuthServerURL:   "https://myaccount.qfei.cn",
		AgentGatewayURL: "https://make-agent.qfei.cn",
		TraceServerURL:  "https://openobserve.qfei.cn",
	},
}

// LookupContext 返回 context preset；name 为空回退 DefaultContext；未知名返回 ok=false。
func LookupContext(name string) (Context, bool) {
	if name == "" {
		name = DefaultContext
	}
	c, ok := contexts[name]
	return c, ok
}

// ContextNames 返回全部合法 context 名，按 lifecycle 顺序（dev → test → production）
// 而非字典序：help / 错误提示 / context list 都沿用这个顺序，读起来才是"晋级路径"。
func ContextNames() []string {
	return []string{"dev", "test", "production"}
}
