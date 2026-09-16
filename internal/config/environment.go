/**
 * [INPUT]: 依赖 sort（标准库）
 * [OUTPUT]: 对外提供 Environment 类型、DefaultEnvironment 常量、LookupEnvironment / EnvironmentNames 函数
 * [POS]: internal/config 的环境拓扑中枢，把 dev/test/production 三套后端 URL 收成一等 preset，作 cmd 层解析链的兜底层
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

import "sort"

// ---------------------------------- 环境 preset ----------------------------------

// Environment 是一个后端环境的 URL preset——把"永远一起出现"的数据泥团收编为对象。
// 均为主机基址（scheme://host），不含路径：
//   - MetaServerURL / RepoServerURL 的网关前缀 /api/make 由 cmd 层 withGateway 统一补齐
//   - AuthServerURL 为身份服务器基址，login 追加 .well-known 路径
//   - AgentGatewayURL 为 Agent 平台 gateway 基址，daemon 直连（无网关前缀）
//   - TraceServerURL 为 OpenObserve 基址，trace 子命令拼 /web/traces/trace-details 直达页
type Environment struct {
	MetaServerURL   string
	RepoServerURL   string
	AuthServerURL   string
	AgentGatewayURL string
	TraceServerURL  string
}

// DefaultEnvironment 是未配置 [settings] environment 时的默认环境（生产已上线，默认收口到 production）。
const DefaultEnvironment = "production"

// environments 是内建环境 preset 表：dev/test 用 qtech.cn（{dev-,test-} 前缀），production 用 qfei.cn。
// OpenObserve 无 dev/test 之分：两个非生产环境共用 openobserve.qtech.cn。
var environments = map[string]Environment{
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

// LookupEnvironment 返回环境 preset；name 为空回退 DefaultEnvironment；未知环境名返回 ok=false。
func LookupEnvironment(name string) (Environment, bool) {
	if name == "" {
		name = DefaultEnvironment
	}
	e, ok := environments[name]
	return e, ok
}

// EnvironmentNames 返回全部合法环境名（字典序），供值校验与错误提示。
func EnvironmentNames() []string {
	names := make([]string, 0, len(environments))
	for name := range environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
