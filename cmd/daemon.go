/**
 * [INPUT]: 依赖 github.com/spf13/cobra、context、os、os/signal、path/filepath、syscall、time、log/slog、internal/config、internal/daemon（主循环）与 internal/daemon/adapter（claude-code / codex backend）
 * [OUTPUT]: 对外提供 daemonCmd——`makecli daemon` 子命令（Hidden：功能未稳定，不对普通用户展示）；
 *           包内提供 runDaemon / runDaemonForeground 与 daemonRunConfig / resolveDaemonRunConfig / newEnrolledDaemon（前台与 launchd 托管共用）
 * [POS]: cmd 模块的设备接入入口：外接 brain 的 daemon 模式——注册设备、claim 领工作、驱动本机 CLI 执行；
 *        缺省即后台（转 runDaemonStart 交 launchd），--foreground 才在当前终端阻塞；
 *        配置 flag > env；setup-key 与 node key 互斥；首次入册换回 node key 落 credentials，普通重启读本地 node key 续连
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/daemon"
	"github.com/qfeius/makecli/internal/daemon/adapter"
	"github.com/spf13/cobra"
)

var (
	daemonGatewayServerURL string
	daemonSetupKey         string
	daemonRuntimeName      string
	daemonWorkDir          string
	daemonMaxRunDuration   time.Duration
	daemonForeground       bool
)

// daemonCmd 是外接 brain 的接入点（agent-design/docs/execution.md §8.1）。
// 缺省即后台：跑完立刻回到提示符，常驻交给 launchd（等价 `daemon start`）；
// --foreground 才在当前终端阻塞——launchd 拉起的正是这一形态。
// stop/restart/status/uninstall 等托管面在 daemon_service.go。
// Hidden：功能未稳定，不在 help 中对普通用户展示；稳定后摘除。
var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "以设备模式接入 Agent 平台,驱动本机 coding CLI 执行任务(缺省后台常驻)",
	Long: `以 runtime 模式接入 Agent 平台:探测本机 coding CLI、入册、心跳 + claim 领工作。

缺省行为是后台常驻——写好 LaunchAgent 交给 launchd 后立刻返回,等价于 makecli daemon start。
要盯着日志调试就加 --foreground,在当前终端阻塞运行(Ctrl-C 退出)。
launchd 托管的进程跑的就是 --foreground 形态:launchd 要求服务自身不得 fork 到后台。`,
	Hidden: true,
	// NoArgs 让 `makecli daemon statsu` 这类拼错直接报"未知子命令"，
	// 而不是被当成位置参数悄悄起了一个 daemon。
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemon(cmd.Context())
	},
}

// runDaemon 决定这次调用是"交给 launchd 后台常驻"还是"就在这个终端跑"。
// 非 macOS 没有托管实现，此时回落前台并明说——把用户拦在门外没有意义。
func runDaemon(ctx context.Context) error {
	if !daemonForeground {
		if hostGOOS == "darwin" {
			return runDaemonStart(ctx)
		}
		fmt.Fprintf(os.Stderr, "当前系统 %s 无后台托管实现(仅 macOS/launchd),改为前台运行;Ctrl-C 退出\n", hostGOOS)
	}
	return runDaemonForeground(ctx)
}

// runDaemonForeground 在当前进程阻塞运行主循环直到 SIGINT/SIGTERM。
func runDaemonForeground(ctx context.Context) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	runConfig, err := resolveDaemonRunConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	agentDaemon, err := newEnrolledDaemonFunc(ctx, runConfig, logger)
	if err != nil {
		return err
	}
	return agentDaemon.Run(ctx)
}

// daemonRunConfig 是 daemon 启动事实的解析结果。
// 收口成一个类型是因为 launchd 托管必须把这些值固化进 plist：
// launchd 拉起的进程没有用户 shell 环境，env 与 preset 的兜底届时都不在了。
type daemonRunConfig struct {
	ServerURL      string
	SetupKey       string
	RuntimeName    string
	WorkDir        string
	MaxRunDuration time.Duration
}

// resolveDaemonRunConfig 把 flag / env / 环境 preset 解析成最终值（缺省一次性补齐）。
func resolveDaemonRunConfig() (daemonRunConfig, error) {
	serverURL, err := resolveAgentGatewayServerURL()
	if err != nil {
		return daemonRunConfig{}, err
	}
	runtimeName := daemonRuntimeName
	if runtimeName == "" {
		runtimeName, _ = os.Hostname()
	}
	workDir := daemonWorkDir
	if workDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return daemonRunConfig{}, err
		}
		workDir = filepath.Join(home, ".make", "agent", "work")
	}
	maxRunDuration := daemonMaxRunDuration
	if maxRunDuration <= 0 {
		maxRunDuration = daemon.DefaultMaxRunDuration
	}
	return daemonRunConfig{
		ServerURL:      serverURL,
		SetupKey:       daemonSetupKey,
		RuntimeName:    runtimeName,
		WorkDir:        workDir,
		MaxRunDuration: maxRunDuration,
	}, nil
}

// newEnrolledDaemon 构造 daemon 并收口入册副作用：
// 未入册时用 setup-key 换回 node key；已入册时复用本地 node key。
// 两种凭证严格互斥：彻底移除 runtime 身份走 daemon uninstall，由它清掉
// 当前 profile 的 node key；重新接入再出示新的 setup-key。
func newEnrolledDaemon(ctx context.Context, runConfig daemonRunConfig, logger *slog.Logger) (*daemon.Daemon, error) {
	creds, err := config.Load()
	if err != nil {
		return nil, err
	}
	nodeKey := creds[Profile].NodeKey
	if nodeKey != "" && runConfig.SetupKey != "" {
		return nil, fmt.Errorf("当前 profile %q 已有 node key；请先运行 `makecli daemon uninstall`，再用新的 setup-key 接入", Profile)
	}

	agentDaemon, err := daemon.New(ctx, daemon.Options{
		ServerURL:      runConfig.ServerURL,
		NodeKey:        nodeKey,
		SetupKey:       runConfig.SetupKey,
		RuntimeName:    runConfig.RuntimeName,
		WorkBaseDir:    runConfig.WorkDir,
		MaxRunDuration: runConfig.MaxRunDuration,
		Backends:       []adapter.Backend{&adapter.ClaudeCode{}, &adapter.Codex{}},
		Logger:         logger,
	})
	if err != nil {
		return nil, err
	}
	if agentDaemon.NodeKey() != nodeKey {
		profile := creds[Profile]
		profile.NodeKey = agentDaemon.NodeKey()
		creds[Profile] = profile
		if err := config.Save(creds); err != nil {
			return nil, fmt.Errorf("持久化 node key 失败: %w", err)
		}
		logger.Info("node key 已保存到 credentials", "profile", Profile)
	}
	return agentDaemon, nil
}

// resolveAgentGatewayServerURL 收口 gateway 地址取值链：
// --gateway-server-url flag > env MAKE_AGENT_SERVER_URL > context preset（随全局 --context）。
// 与其余子命令的 URL 解析纪律一致——用户缺省零配置连对后端。
func resolveAgentGatewayServerURL() (string, error) {
	if daemonGatewayServerURL != "" {
		return daemonGatewayServerURL, nil
	}
	if fromEnv := os.Getenv("MAKE_AGENT_SERVER_URL"); fromEnv != "" {
		return fromEnv, nil
	}
	_, c, err := resolveContext()
	if err != nil {
		return "", err
	}
	return c.AgentGatewayURL, nil
}

func init() {
	// PersistentFlags：start 子命令要用完全同一套旋钮把前台形态固化进 plist，
	// 定义一次即父子共享，杜绝两处 flag 定义漂移。
	daemonCmd.PersistentFlags().StringVar(&daemonGatewayServerURL, "gateway-server-url", "", "Agent 平台 gateway 地址(缺省 MAKE_AGENT_SERVER_URL,再缺省按 --context preset)")
	daemonCmd.PersistentFlags().StringVar(&daemonSetupKey, "setup-key", "", "首次入册用的一次性 setup-key(与本地 node key 互斥；重新入册前先 daemon uninstall)")
	daemonCmd.PersistentFlags().StringVar(&daemonRuntimeName, "name", "", "runtime 名(缺省取 hostname)")
	daemonCmd.PersistentFlags().StringVar(&daemonWorkDir, "work-dir", "", "工作目录根(缺省 ~/.make/agent/work)")
	daemonCmd.PersistentFlags().DurationVar(&daemonMaxRunDuration, "max-run-duration", daemon.DefaultMaxRunDuration, "单 run 时长兜底")
	// 本地 flag：只属于 `makecli daemon` 自身，start/stop 等子命令不继承（对它们没有意义）。
	daemonCmd.Flags().BoolVar(&daemonForeground, "foreground", false, "在当前终端前台运行(缺省交给 launchd 后台常驻)")
	rootCmd.AddCommand(daemonCmd)
}
