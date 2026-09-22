/**
 * [INPUT]: 依赖 internal/config，bufio、encoding/base64、fmt、os、strings、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newConfigureCmd 函数（含 token/config/set/get/verify/resolve 子命令）；包内 sampleConfig 模板、validConfigKeys、rejectSettingKey
 * [POS]: cmd 模块的 configure 命令组，交互式或直接写入 ~/.make/credentials 和 ~/.make/config 的 profile 段；
 *        context 同时支持 profile 默认值与全局默认值；set/get 收到仅限全局的键名时经 rejectSettingKey 报错并指路，不静默转发
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/qfeius/makecli/internal/config"
	"github.com/spf13/cobra"
)

// ---------------------------------- sample 模板 ----------------------------------

// sampleConfig 是 `configure --sample` 输出的带注释 INI 参考模板，展示全部可配置项。
// profile 覆盖键以占位值平铺展示（非注释），当参考卡用——直写需替换占位主机名或删行回退
// 环境 preset。新增可配置键须同步此处，configure_test.go 的完整性测试守护其不漏键、活跃值
// 经真实 loader 校验。
const sampleConfig = `# MakeCLI configuration reference - every available key with placeholder values.
# Location: ~/.make/config   (override the directory with $MAKE_CLI_CONFIG_DIR)
#
# Redirect to create a config, then edit the placeholder values:
#     makecli configure --sample > ~/.make/config
# Or set keys one by one:
#     makecli settings set <key> <value>      # [settings] keys
#     makecli configure set <key> <value>     # profile keys
#
# Access tokens are NOT here - they live in ~/.make/credentials
# (set one with: makecli configure token   or: makecli login).

# ===== Global settings (shared by every profile) =====
[settings]
# Active backend context (which Make backend to talk to). One of: dev, test, production
# Switch with: makecli context use <name>   (or: makecli settings set context <name>)
context = dev
# Auto-update notifier. true | false
check-for-updates = true
# Release channel for updates and the update notifier. One of: stable, beta
channel = stable
# Optional. role = user installs only the makecli skill (manage resources, no app development);
# leave it unset to install every skill. Set with: makecli skills install --role user
# role = user

# ===== Profile: default (select another with --profile <name>) =====
# These override the context preset and are optional - replace the
# placeholders, or delete a line to fall back to the preset for that host.
[default]
# Default backend for this profile; --context and MAKE_CLI_CONTEXT override it
context = dev
# Meta Server host (the gateway prefix /api/make is added automatically)
meta-server-url = meta.dev.example.com
# Code Repository Server host
repo-server-url = repo.dev.example.com
# OAuth identity server base (used by the login command)
auth-server-url = myaccount.dev.example.com
# Tenant / operator headers injected on every outbound request
X-Tenant-ID = your-tenant-id
X-Operator-ID = your-operator-id
`

// ---------------------------------- 命令组 ----------------------------------

func newConfigureCmd() *cobra.Command {
	var sample bool
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure MakeCLI credentials and settings",
		Example: `  # interactive access-token setup (default action)
  makecli configure

  # print a sample config showing every available key
  makecli configure --sample

  # non-interactively set / read a single profile value
  makecli configure set X-Tenant-ID 1024
  makecli configure get meta-server-url`,
		SilenceUsage: true,
		// 所有 configure 子命令统一前置校验 profile 名（settings 为保留段名，不可作 profile）
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return config.ValidateProfileName(Profile)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if sample {
				fmt.Print(sampleConfig)
				return nil
			}
			return runConfigureToken()
		},
	}

	cmd.Flags().BoolVar(&sample, "sample", false, "print a sample ~/.make/config to stdout and exit")

	cmd.AddCommand(newConfigureTokenCmd())
	cmd.AddCommand(newConfigureConfigCmd())
	cmd.AddCommand(newConfigureSetCmd())
	cmd.AddCommand(newConfigureGetCmd())
	cmd.AddCommand(newConfigureVerifyCmd())
	cmd.AddCommand(newConfigureResolveCmd())

	return cmd
}

// ---------------------------------- token 子命令 ----------------------------------

func newConfigureTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "token",
		Short:        "Configure access token (writes to ~/.make/credentials)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureToken()
		},
	}
}

func runConfigureToken() error {
	creds, err := config.Load()
	if err != nil {
		return err
	}

	current := creds[Profile]

	fmt.Printf("Configuring profile [%s]\n", Profile)

	token, err := prompt("MakeCLI Access Token", current.AccessToken)
	if err != nil {
		return err
	}
	if token != "" {
		if err := validateJWT(token); err != nil {
			return err
		}
		current.AccessToken = token
	}

	creds[Profile] = current
	if err := config.Save(creds); err != nil {
		return err
	}

	path, _ := config.CredentialsPath()
	fmt.Printf("\nCredentials saved to %s\n", path)
	return nil
}

// ---------------------------------- config 子命令 ----------------------------------

func newConfigureConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "config",
		Short:        "Configure custom headers (writes to ~/.make/config)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureConfig()
		},
	}
}

func runConfigureConfig() error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	current := cfg[Profile]
	fmt.Printf("Configuring config profile [%s]\n", Profile)

	serverURL, err := prompt("meta-server-url", current.MetaServerURL)
	if err != nil {
		return err
	}
	if serverURL != "" {
		current.MetaServerURL = serverURL
	}

	repoServerURL, err := prompt("repo-server-url", current.RepoServerURL)
	if err != nil {
		return err
	}
	if repoServerURL != "" {
		current.RepoServerURL = repoServerURL
	}

	authServerURL, err := prompt("auth-server-url", current.AuthServerURL)
	if err != nil {
		return err
	}
	if authServerURL != "" {
		current.AuthServerURL = authServerURL
	}

	tenantID, err := prompt("X-Tenant-ID", current.XTenantID)
	if err != nil {
		return err
	}
	if tenantID != "" {
		current.XTenantID = tenantID
	}

	operatorID, err := prompt("X-Operator-ID", current.OperatorID)
	if err != nil {
		return err
	}
	if operatorID != "" {
		current.OperatorID = operatorID
	}

	cfg[Profile] = current
	if err := config.SaveConfig(cfg); err != nil {
		return err
	}

	path, _ := config.ConfigPath()
	fmt.Printf("\nConfig saved to %s\n", path)
	return nil
}

// ---------------------------------- set 子命令 ----------------------------------

var validConfigKeys = []string{"context", "meta-server-url", "repo-server-url", "auth-server-url", "X-Tenant-ID", "X-Operator-ID"}

func validateConfigKey(key string) error {
	if slices.Contains(validConfigKeys, key) {
		return nil
	}
	return fmt.Errorf("unknown config key '%s', valid keys: %s", key, strings.Join(validConfigKeys, ", "))
}

// rejectSettingKey 把误投到 configure 的全局键指路到 settings 命令，不做静默转发（保持一条路）。
func rejectSettingKey(key, verb string) error {
	if isSettingKey(key) && !slices.Contains(validConfigKeys, key) {
		return fmt.Errorf("%q is a global setting, not a profile key; use: makecli settings %s %s", key, verb, key)
	}
	return nil
}

func newConfigureSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a profile config value (writes to ~/.make/config)",
		Long: fmt.Sprintf(`Set a single value in the current --profile section as "<key> <value>".

Profile keys: %s
Global settings (%s) are managed by "makecli settings".`,
			strings.Join(validConfigKeys, ", "),
			strings.Join(settingKeyNames(), ", ")),
		Example: `  # point the current profile at a custom meta server host
  makecli configure set meta-server-url meta.dev.example.com

  # set the default backend for a specific profile
  makecli --profile staging configure set context test

  # set the tenant header for a specific profile
  makecli --profile staging configure set X-Tenant-ID 1024`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureSet(args[0], args[1])
		},
	}
}

func runConfigureSet(key, value string) error {
	if err := rejectSettingKey(key, "set"); err != nil {
		return err
	}
	if err := validateConfigKey(key); err != nil {
		return err
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	p := cfg[Profile]
	switch key {
	case "context":
		if value != "" && !slices.Contains(config.ContextNames(), value) {
			return fmt.Errorf("unknown context %q, valid: %s", value, strings.Join(config.ContextNames(), ", "))
		}
		p.Context = value
	case "meta-server-url":
		p.MetaServerURL = value
	case "repo-server-url":
		p.RepoServerURL = value
	case "auth-server-url":
		p.AuthServerURL = value
	case "X-Tenant-ID":
		p.XTenantID = value
	case "X-Operator-ID":
		p.OperatorID = value
	}
	cfg[Profile] = p
	return config.SaveConfig(cfg)
}

// ---------------------------------- get 子命令 ----------------------------------

func newConfigureGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a profile config value from ~/.make/config",
		Long: fmt.Sprintf(`Read a single value from the current --profile section.

Profile keys: %s
Global settings (%s) are read with "makecli settings get".`,
			strings.Join(validConfigKeys, ", "),
			strings.Join(settingKeyNames(), ", ")),
		Example: `  makecli configure get meta-server-url
  makecli --profile staging configure get X-Tenant-ID`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigureGet(args[0])
		},
	}
}

func runConfigureGet(key string) error {
	if err := rejectSettingKey(key, "get"); err != nil {
		return err
	}
	if err := validateConfigKey(key); err != nil {
		return err
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	p := cfg[Profile]
	switch key {
	case "context":
		fmt.Println(p.Context)
	case "meta-server-url":
		fmt.Println(p.MetaServerURL)
	case "repo-server-url":
		fmt.Println(p.RepoServerURL)
	case "auth-server-url":
		fmt.Println(p.AuthServerURL)
	case "X-Tenant-ID":
		fmt.Println(p.XTenantID)
	case "X-Operator-ID":
		fmt.Println(p.OperatorID)
	}
	return nil
}

// ---------------------------------- 共用 helpers ----------------------------------

// prompt 打印提示行（已有值则遮掩末尾4位显示），读取用户输入
// 用户直接回车表示保留当前值，返回空字符串
func prompt(label, current string) (string, error) {
	hint := "None"
	if current != "" {
		hint = mask(current)
	}
	fmt.Printf("%s [%s]: ", label, hint)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// mask 保留末尾4位，其余替换为 *
// 短于4位则全部遮掩
func mask(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	return strings.Repeat("*", len(s)-4) + s[len(s)-4:]
}

// validateJWT 校验 token 是否符合 JWT 格式（三段 base64url，不验证签名）
func validateJWT(token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid token format: expected JWT (3 base64url segments separated by '.')")
	}
	for i, part := range parts {
		if _, err := base64.RawURLEncoding.DecodeString(part); err != nil {
			return fmt.Errorf("invalid token format: segment %d is not valid base64url", i+1)
		}
	}
	return nil
}
