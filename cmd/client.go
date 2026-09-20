/**
 * [INPUT]: 依赖 internal/config（Load/LoadConfig/LoadSettings/LookupContext）、internal/api（New/Option/WithDebug/DebugFormat/WithHeaders）、fmt、os、slices、strings；从 root.go 读取全局 Profile / AccessToken / MetaServerURL / Context / DebugMode，从 output.go 读取 resolvedOutput
 * [OUTPUT]: 对外提供 newClientFromProfile（变参 ...api.Option）/ newRepoClientFromProfile / debugOption（--debug × --output 合成 api.WithDebug）/ resolveAccessToken / accessTokenSource / metaServerURL / repoServerURL / resolveContext / contextName / resolveChannel / resolveRole 函数、withGateway helper、apiGatewayPath / EnvAccessToken / EnvMetaServerURL / EnvRepoServerURL / EnvContext 常量与 tokenSource 常量
 * [POS]: cmd 模块的公共 helper，统一「全局命令行入参 → API 客户端」的构建逻辑——profile / token / server / context / debug 全部由 root PersistentFlag 注入，子命令零参数调用；
 *        newClientFromProfile 收 ...api.Option 变参，把每命令横切选项（如 WithDryRun）追加到基础选项之后，写命令按需注入；
 *        resolveAccessToken 是 token 取值链的唯一入口：--access-token flag > $MAKE_ACCESS_TOKEN > credentials[profile].access_token（resolveProfile / configure verify / whoami 共用，不允许第二条链）；
 *        resolveContext 是后端 context 解析链的唯一入口：--context > $MAKE_CLI_CONTEXT > [settings] context > production，旧键 environment 未迁移即报错指引 doctor（login / trace / daemon / configure resolve|verify / context show 共用，不允许第二条链）；
 *        resolveProfile 收口凭证与配置解析；主机地址取值链 metaServerURL：flag > $MAKE_META_SERVER_URL > profile config > context 内置地址；repoServerURL 同构但无 flag 级（configure resolve / verify 同用，不允许手写第二条链），主机基址再经 withGateway 补网关前缀 /api/make
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/qfeius/makecli/internal/api"
	"github.com/qfeius/makecli/internal/config"
)

// EnvAccessToken 是 access token 的环境变量名。命名对齐 flag --access-token 与
// credentials 键 access_token 三者同名；前缀取 MAKE_（平台凭证族，同 MAKE_AGENT_TOKEN），
// 而非 MAKE_CLI_（工具自身行为开关族，如 MAKE_CLI_CONFIG_DIR）。
const EnvAccessToken = "MAKE_ACCESS_TOKEN"

// token 来源标识：configure verify 的 JSON source 字段与鉴权失败引导共用，
// 让「为什么用的不是我想的那把 token」一眼可定位。
const (
	tokenSourceFlag        = "flag"
	tokenSourceEnv         = "env"
	tokenSourceCredentials = "credentials"
)

// accessTokenSource 只回答 token 来自哪里，不读盘、不会失败：
// --access-token flag > $MAKE_ACCESS_TOKEN > credentials 文件。
// flag 与 env 都为空时落到 credentials——此时文件里有没有 token 是 resolveAccessToken 的事。
func accessTokenSource() string {
	switch {
	case AccessToken != "":
		return tokenSourceFlag
	case os.Getenv(EnvAccessToken) != "":
		return tokenSourceEnv
	default:
		return tokenSourceCredentials
	}
}

// resolveAccessToken 是 token 取值链的唯一入口，返回 token 与其来源。
// flag / env 覆盖时不碰凭证文件（CI 里可以完全没有 ~/.make）；
// 三处都没有时返回空 token + credentials 来源，由调用方决定是报错还是引导登录。
func resolveAccessToken() (string, string, error) {
	switch src := accessTokenSource(); src {
	case tokenSourceFlag:
		return AccessToken, src, nil
	case tokenSourceEnv:
		return os.Getenv(EnvAccessToken), src, nil
	}
	creds, err := config.Load()
	if err != nil {
		return "", "", fmt.Errorf("加载凭证失败: %w", err)
	}
	return creds[Profile].AccessToken, tokenSourceCredentials, nil
}

// resolveProfile 按当前全局 Profile 读取凭证与配置，返回 token、profile 配置与附加 headers。
// token 走 resolveAccessToken（flag / env 覆盖只替换凭证本身，tenant / operator / URL 仍取自 profile config）
func resolveProfile() (string, config.ConfigProfile, map[string]string, error) {
	if err := config.ValidateProfileName(Profile); err != nil {
		return "", config.ConfigProfile{}, nil, err
	}

	token, _, err := resolveAccessToken()
	if err != nil {
		return "", config.ConfigProfile{}, nil, err
	}
	if token == "" {
		return "", config.ConfigProfile{}, nil, fmt.Errorf("profile '%s' 未配置，请先运行: makecli login --profile %s（或设置 $%s）", Profile, Profile, EnvAccessToken)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return "", config.ConfigProfile{}, nil, fmt.Errorf("加载配置失败: %w", err)
	}

	cp := cfg[Profile]
	headers := map[string]string{}
	if cp.XTenantID != "" {
		headers["X-Tenant-ID"] = cp.XTenantID
	}
	if cp.OperatorID != "" {
		headers["X-Operator-ID"] = cp.OperatorID
	}
	return token, cp, headers, nil
}

// 后端主机地址的环境变量名，与 flag / profile config 键同名（meta-server-url / repo-server-url），
// 前缀取 MAKE_（平台端点族），与 EnvAccessToken 同一命名规则。
const (
	EnvMetaServerURL = "MAKE_META_SERVER_URL"
	EnvRepoServerURL = "MAKE_REPO_SERVER_URL"
)

// metaServerURL / repoServerURL 是主机地址取值链的唯一入口，与 resolveAccessToken 同构：
// flag > env > profile config > 当前 context 内置地址（最后一级是默认值而非配置项）。
// 返回裸主机基址，网关前缀由调用方经 withGateway 补齐。
func metaServerURL(cp config.ConfigProfile, c config.Context) string {
	return firstNonEmpty(MetaServerURL, os.Getenv(EnvMetaServerURL), cp.MetaServerURL, c.MetaServerURL)
}

// repoServerURL 无 flag 级：代码仓库主机是部署实现细节，不值得占一个全局 flag；$MAKE_REPO_SERVER_URL 仍留给 CI/测试覆盖
func repoServerURL(cp config.ConfigProfile, c config.Context) string {
	return firstNonEmpty(os.Getenv(EnvRepoServerURL), cp.RepoServerURL, c.RepoServerURL)
}

// firstNonEmpty 返回第一个非空字符串，统一「flag > env > config > context preset」取值链
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// apiGatewayPath 是后端网关挂载点，Meta / Data / Integration / Repo 请求共用此前缀。
// 主机基址（preset / config / flag）只描述部署端点，网关前缀由代码统一补齐，
// 使配置可写成纯主机名（https://test-make.qtech.cn）而非完整 URL。
const apiGatewayPath = "/api/make"

// withGateway 把网关前缀补到解析出的主机基址后。
// 幂等：已含前缀的完整 URL 原样返回，消除「配置该不该带 /api/make」的特殊情况；
// 空串原样返回（交由 api 层报错），尾随斜杠先行裁掉避免双斜杠。
func withGateway(host string) string {
	host = strings.TrimRight(host, "/")
	if host == "" || strings.HasSuffix(host, apiGatewayPath) {
		return host
	}
	return host + apiGatewayPath
}

// EnvContext 是后端 context 的环境变量名（会话级作用域，介于 --context 与 [settings] 之间）。
// 前缀取 MAKE_CLI_：选哪套后端是工具自身行为，与 MAKE_CLI_CONFIG_DIR 同族，而非 MAKE_ 平台凭证族。
const EnvContext = "MAKE_CLI_CONTEXT"

// resolveContext 是后端 context 解析链的唯一入口，返回 context 名与其 preset：
// --context flag > $MAKE_CLI_CONTEXT > [settings] context > DefaultContext。
// 配置文件仍在用旧键（[settings] environment）时拒绝解析并指引 doctor --fix——
// 不做静默回退：旧配置写着 dev 却悄悄落到 production 是最坏的兼容方式。
// 未知 context 名（typo / 非法手抄）同样报错，避免静默落到错误后端。
func resolveContext() (string, config.Context, error) {
	name := firstNonEmpty(Context, os.Getenv(EnvContext))
	if name == "" {
		settings, err := config.LoadSettings()
		if err != nil {
			return "", config.Context{}, err
		}
		if len(settings.Legacy) > 0 {
			return "", config.Context{}, fmt.Errorf("config is outdated ([settings] %s); run: %s", strings.Join(sortedKeys(settings.Legacy), ", "), doctorFixHint)
		}
		name = firstNonEmpty(settings.Context, config.DefaultContext)
	}
	c, ok := config.LookupContext(name)
	if !ok {
		return "", config.Context{}, fmt.Errorf("unknown context %q, valid: %s", name, strings.Join(config.ContextNames(), ", "))
	}
	return name, c, nil
}

// contextName 是 resolveContext 的纯展示姊妹：解析失败时回显 "unknown" 而非报错——
// 展示场景（鉴权失败引导、whoami）不该因配置问题而无名可显，也不该谎报一个 context。
func contextName() string {
	name, _, err := resolveContext()
	if err != nil {
		return "unknown"
	}
	return name
}

// sortedKeys 返回 map 的键（字典序），供错误提示稳定输出
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// resolveChannel 收口发布通道解析：[settings] channel > DefaultChannel。
// 未知通道名报错（对齐 resolveContext 的未知名报错先例；notifier 侧
// 的静默回退是另一职责，见 internal/notifier channelOf）。
func resolveChannel() (string, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return "", err
	}
	if settings.Channel == "" {
		return config.DefaultChannel, nil
	}
	if !slices.Contains(config.ChannelNames(), settings.Channel) {
		return "", fmt.Errorf("unknown channel '%s' in config, valid: %s",
			settings.Channel, strings.Join(config.ChannelNames(), ", "))
	}
	return settings.Channel, nil
}

// resolveRole 收口角色解析：[settings] role，未设置返回空串（= 原有行为，skills 全量）。
// 未知角色名报错（与 resolveChannel 同款），被 update 后置同步与 skills list 消费。
func resolveRole() (string, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return "", err
	}
	if settings.Role == "" {
		return "", nil
	}
	if !slices.Contains(config.RoleNames(), settings.Role) {
		return "", fmt.Errorf("unknown role '%s' in config, valid: %s",
			settings.Role, strings.Join(config.RoleNames(), ", "))
	}
	return settings.Role, nil
}

// newClientFromProfile 构建指向 Meta/Data Server 的 API 客户端。
// profile / server / env / debug 四个全局态都来自 rootCmd 的 PersistentFlag，子命令无需也不应再传 profile。
// extra 是可选的每命令横切选项（如 WithDryRun）：基础选项之后追加，由具体写命令按需注入。
func newClientFromProfile(extra ...api.Option) (*api.Client, error) {
	token, cp, headers, err := resolveProfile()
	if err != nil {
		return nil, err
	}
	_, c, err := resolveContext()
	if err != nil {
		return nil, err
	}
	server := withGateway(metaServerURL(cp, c))
	opts := append([]api.Option{debugOption(), api.WithHeaders(headers)}, extra...)
	return api.New(server, token, opts...), nil
}

// debugOption 把全局 --debug 与被调命令的 --output 合成 api 的调试选项：
// 输出走 JSON（显式 --output json，或 auto 落到管道/agent）时调试转储也用 JSON，人在终端看 curl -v 文本
func debugOption() api.Option {
	format := api.DebugText
	if resolvedOutput == outputJSON {
		format = api.DebugJSON
	}
	return api.WithDebug(DebugMode, format)
}

// newRepoClientFromProfile 构建指向代码仓库服务（make-repo）的 API 客户端。
// 额外返回裸 token，供 deploy 的 git push HTTP Basic 认证使用。
func newRepoClientFromProfile() (*api.Client, string, error) {
	token, cp, headers, err := resolveProfile()
	if err != nil {
		return nil, "", err
	}
	_, c, err := resolveContext()
	if err != nil {
		return nil, "", err
	}
	server := withGateway(repoServerURL(cp, c))
	return api.New(server, token, debugOption(), api.WithHeaders(headers)), token, nil
}
