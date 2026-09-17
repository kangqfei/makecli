/**
 * [INPUT]: 依赖 internal/config（LoadConfig）、internal/api（New/WithHeaders）、cmd/client（resolveAccessToken/metaServerURL/resolveEnvironment/tokenSource 常量/EnvAccessToken）、cmd/output（outputJSON/resolveOutputFormat/writeJSON）、encoding/base64、encoding/json、fmt、os、strings、time
 * [OUTPUT]: 对外提供 newConfigureVerifyCmd 函数；包内 parseJWTTimeClaims 免验签提取 iat/exp、renewTokenHint 按 token 来源给换 token 指引
 * [POS]: cmd/configure 的 verify 子命令，token 走 resolveAccessToken 取值链（结果带 source 字段），本地 exp fail-closed 判定 + 在线验证 token 有效性并输出 profile 状态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qfeius/makecli/internal/api"
	"github.com/qfeius/makecli/internal/config"
	"github.com/spf13/cobra"
)

func newConfigureVerifyCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:          "verify",
		Short:        "Verify that the current profile has a valid token",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := runConfigureVerify(output)
			if err != nil {
				return err
			}
			if !r.Valid {
				os.Exit(1)
			}
			return nil
		},
	}

	addOutputFlag(cmd, &output)
	return cmd
}

// verifyResult 承载验证结果，复用于 table 和 JSON 输出
type verifyResult struct {
	Profile       string `json:"profile"`
	Valid         bool   `json:"valid"`
	Source        string `json:"source"`
	Token         string `json:"token"`
	IssuedAt      string `json:"issued_at"`
	ExpiresAt     string `json:"expires_at"`
	MetaServerURL string `json:"meta_server_url"`
	TenantID      string `json:"tenant_id"`
	OperatorID    string `json:"operator_id"`
	Message       string `json:"message"`
}

// parseJWTTimeClaims 免验签提取 JWT payload 中的 iat/exp——过期判定不依赖签名真伪：
// exp 已过则无论签名是否有效 token 都不可用，这正是 fail-closed 的依据；
// 缺失的 claim 返回零值 time.Time，由调用方按「无法判定」处理
func parseJWTTimeClaims(token string) (issuedAt, expiresAt time.Time, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, time.Time{}, fmt.Errorf("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	var claims struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if claims.Iat > 0 {
		issuedAt = time.Unix(claims.Iat, 0)
	}
	if claims.Exp > 0 {
		expiresAt = time.Unix(claims.Exp, 0)
	}
	return issuedAt, expiresAt, nil
}

func runConfigureVerify(output string) (*verifyResult, error) {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return nil, err
	}

	result := verifyResult{Profile: Profile}

	// 取 token：flag > env > credentials（resolveAccessToken 唯一链），来源随结果输出
	token, source, err := resolveAccessToken()
	if err != nil {
		return nil, err
	}
	result.Source = source

	// 加载配置（meta-server-url / tenant / operator）
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}
	cp := cfg[Profile]
	result.MetaServerURL = cp.MetaServerURL
	result.TenantID = cp.XTenantID
	result.OperatorID = cp.OperatorID

	// 检查 token 是否存在
	if token == "" {
		result.Message = "token not configured"
		outputVerifyResult(&result, output)
		return &result, nil
	}
	result.Token = mask(token)

	// JWT 格式校验
	if err := validateJWT(token); err != nil {
		result.Message = "token invalid (malformed JWT)"
		outputVerifyResult(&result, output)
		return &result, nil
	}

	// 本地 exp 判定（fail-closed）：已过期不触网直接判无效；
	// payload 不可解析或无 exp claim 时不下本地结论，交给在线验证
	if issuedAt, expiresAt, err := parseJWTTimeClaims(token); err == nil {
		if !issuedAt.IsZero() {
			result.IssuedAt = issuedAt.Format(time.RFC3339)
		}
		if !expiresAt.IsZero() {
			result.ExpiresAt = expiresAt.Format(time.RFC3339)
			if time.Now().After(expiresAt) {
				result.Message = "token expired. " + renewTokenHint(source)
				outputVerifyResult(&result, output)
				return &result, nil
			}
		}
	}

	// 在线验证：调用 app list(page=1, size=1)；server 走 metaServerURL 取值链
	env, err := resolveEnvironment()
	if err != nil {
		return nil, err
	}
	server := metaServerURL(cp, env)
	headers := map[string]string{}
	if result.TenantID != "" {
		headers["X-Tenant-ID"] = result.TenantID
	}
	if result.OperatorID != "" {
		headers["X-Operator-ID"] = result.OperatorID
	}

	client := api.New(withGateway(server), token, debugOption(), api.WithHeaders(headers))
	_, _, err = client.ListApps(1, 1, "")
	if err != nil {
		result.Message = fmt.Sprintf("token invalid (%s)", err)
		outputVerifyResult(&result, output)
		return &result, nil
	}

	result.Valid = true
	result.Message = "ok"
	outputVerifyResult(&result, output)
	return &result, nil
}

func outputVerifyResult(r *verifyResult, output string) {
	switch {
	case output == outputJSON:
		_ = writeJSON(r)
	case r.Valid:
		fmt.Printf("Profile [%s]: ok\n", r.Profile)
	default:
		fmt.Printf("Profile [%s]: %s\n", r.Profile, r.Message)
		fmt.Fprintf(os.Stderr, "\n%s\n", renewTokenHint(r.Source))
	}
}

// renewTokenHint 按 token 来源给出换 token 的 next-step：来自 credentials 文件时 login 即可；
// 来自 flag / env 时 login 写下的凭证会被覆盖值遮蔽，必须换掉或撤掉覆盖值本身。
func renewTokenHint(source string) string {
	switch source {
	case tokenSourceFlag:
		return "Token came from --access-token; pass a valid one or drop the flag to fall back to credentials."
	case tokenSourceEnv:
		return fmt.Sprintf("Token came from $%s; export a valid one or unset it to fall back to credentials.", EnvAccessToken)
	default:
		return fmt.Sprintf("Run \"makecli login --profile %s\" to renew token.", Profile)
	}
}
