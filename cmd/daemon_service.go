/**
 * [INPUT]: 依赖 github.com/spf13/cobra、context、errors、fmt、log/slog、os、path/filepath、runtime、strings、
 *          internal/config（配置目录/日志目录）、internal/daemon/launchd（LaunchAgent 托管原语）、daemon.go（配置解析与入册）
 * [OUTPUT]: 对外提供 daemon start / stop / restart / status / uninstall 五个子命令（挂在 daemonCmd 下）
 * [POS]: cmd 模块的 daemon 托管面：把前台 `makecli daemon` 交给 macOS launchd 常驻（登录自启 + 崩溃拉起）；
 *        平台守卫 hostGOOS 与 launchd 原语均为包级变量，单测无需 macOS 也能跑
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/daemon/launchd"
	"github.com/spf13/cobra"
)

// hostGOOS 是平台守卫的判定源（包级变量便于单测在非 macOS 上覆盖）。
var hostGOOS = runtime.GOOS

// launchd 托管原语的可打桩出口——单测据此隔离 launchctl 调用与 ~/Library 写入。
var (
	launchdInstall        = launchd.Install
	launchdStop           = launchd.Stop
	launchdUninstall      = launchd.Uninstall
	launchdRestart        = launchd.Restart
	launchdQuery          = launchd.Query
	newEnrolledDaemonFunc = newEnrolledDaemon
)

// daemonStatusOutput 承接 status 的 --output。
var daemonStatusOutput string

// daemon 托管状态的三态（JSON 用下划线形态，便于脚本判等）。
const (
	daemonStateRunning      = "running"
	daemonStateStopped      = "stopped"
	daemonStateNotInstalled = "not installed"
)

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "把 daemon 交给 launchd 常驻(macOS,登录自启 + 崩溃拉起)",
	Long: `把前台 makecli daemon 固化成用户级 LaunchAgent 并立即拉起。

flag 与前台形态完全一致,取值在 start 时一次性解析并写进 plist——
launchd 拉起的进程不继承 shell 环境,所以 gateway 地址、PATH 等都以此刻的解析结果为准。
改了 flag 重跑 start 即覆盖生效。`,
	// 这里的失败都是运行期错误(平台不支持/前置探测/launchctl),不是用法错误——不刷 usage。
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemonStart(cmd.Context())
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "停止 launchd 托管的 daemon(保留托管配置)",
	Long: `停止 daemon,但保留 LaunchAgent plist。

停用是持久的:光卸载的话,下次登录 launchd 扫描 ~/Library/LaunchAgents
又会把它拉起来,所以 stop 顺手写下 launchctl disable 记录。
再次拉起用 makecli daemon start 或 restart;彻底移除托管用 makecli daemon uninstall。`,
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemonStop()
	},
}

var daemonUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "彻底移除 daemon(停服 + 删除 LaunchAgent 与当前 profile 的 node key)",
	Long: `停止 daemon 并删除 LaunchAgent plist,同时清掉 stop 留下的 disable 记录。

同时删除当前 profile 在 ~/.make/credentials 里的 node key,不保留旧 runtime
身份。日志、工作目录与同 profile 的 access token 原样保留;重新接入必须使用
新的 setup-key。`,
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemonUninstall()
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:          "restart",
	Short:        "重启 launchd 托管的 daemon(沿用 start 时敲定的参数)",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemonRestart()
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "查看 launchd 托管的 daemon 状态",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemonStatus()
	},
}

// daemonStatusResult 是 status 的输出投影（table 与 json 同源）。
type daemonStatusResult struct {
	Label          string   `json:"label"`
	State          string   `json:"state"`
	Disabled       bool     `json:"disabled"` // 被 stop 停用：登录时不会自启
	PID            int      `json:"pid,omitempty"`
	LastExitStatus *int     `json:"last_exit_status,omitempty"`
	PlistPath      string   `json:"plist_path"`
	Command        []string `json:"command,omitempty"`
	LogPath        string   `json:"log_path,omitempty"`
}

// ensureLaunchdSupported 是平台守卫：托管面 v1 只做 macOS/launchd，
// 其余平台明确报错并指回前台形态，而不是装作支持后在别处失败。
func ensureLaunchdSupported(action string) error {
	if hostGOOS != "darwin" {
		return fmt.Errorf("daemon %s 目前只支持 macOS(launchd 托管),当前系统为 %s；请前台运行 `makecli daemon`", action, hostGOOS)
	}
	return nil
}

// daemonLogPath 是托管进程的日志落点（stdout 与 stderr 合流）。
// 放在配置目录下与 ~/.make/agent/work 同族，随 MAKE_CLI_CONFIG_DIR 一起搬家。
func daemonLogPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent", "logs", "daemon.log"), nil
}

// runDaemonStart 先做真实前置（探测 CLI + 必要时入册），再把前台形态固化进 plist。
func runDaemonStart(ctx context.Context) error {
	if err := ensureLaunchdSupported("start"); err != nil {
		return err
	}
	runConfig, err := resolveDaemonRunConfig()
	if err != nil {
		return err
	}
	// 前置跑一次真实构造：探测本机 coding CLI、必要时用 setup-key 换回 node key 落盘。
	// 失败必须现在就在终端报出来——交给 launchd 之后 KeepAlive 只会把同一个错误
	// 埋进日志里反复重启，用户看到的是"启动成功但什么也没发生"。
	preflightLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if _, err := newEnrolledDaemonFunc(ctx, runConfig, preflightLogger); err != nil {
		return err
	}

	agentConfig, err := buildLaunchdConfig(runConfig)
	if err != nil {
		return err
	}
	plistPath, err := launchdInstall(agentConfig)
	if err != nil {
		return err
	}

	fmt.Printf("%-10s %s\n", "Runtime", runConfig.RuntimeName)
	fmt.Printf("%-10s %s\n", "Gateway", runConfig.ServerURL)
	fmt.Printf("%-10s %s\n", "Work dir", runConfig.WorkDir)
	fmt.Printf("%-10s %s\n", "Plist", plistPath)
	fmt.Printf("%-10s %s\n", "Logs", agentConfig.LogPath)
	fmt.Println("daemon 已在后台运行(launchd 托管:登录自启,退出自动拉起)")
	fmt.Println("看状态: makecli daemon status;盯日志: tail -f " + agentConfig.LogPath)
	return nil
}

// buildLaunchdConfig 把解析后的启动事实翻译成 plist 所需的命令行与环境。
func buildLaunchdConfig(runConfig daemonRunConfig) (launchd.Config, error) {
	binaryPath, err := os.Executable()
	if err != nil {
		return launchd.Config{}, fmt.Errorf("定位 makecli 可执行文件失败: %w", err)
	}
	// brew 装的 makecli 在 PATH 上是软链；写实路径进 plist，升级换链不影响已托管服务。
	if resolved, err := filepath.EvalSymlinks(binaryPath); err == nil {
		binaryPath = resolved
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return launchd.Config{}, fmt.Errorf("无法获取 home 目录: %w", err)
	}
	logPath, err := daemonLogPath()
	if err != nil {
		return launchd.Config{}, err
	}

	// PATH 必须显式带上：launchd 只给最小 PATH，claude / codex 常装在
	// ~/.local/bin、npm 全局 bin 或 homebrew 下，缺了就是"探测不到 brain CLI"。
	env := map[string]string{
		"PATH": os.Getenv("PATH"),
		"HOME": home,
	}
	if configDir := os.Getenv(config.EnvConfigDir); configDir != "" {
		env[config.EnvConfigDir] = configDir
	}

	// 全局 flag 在子命令前，与用户手敲的形态一致；--env 不必带——
	// gateway 地址已解析成绝对值写进参数，不再依赖环境 preset。
	//
	// --foreground 不可省：`makecli daemon` 缺省是"托管到 launchd"，
	// 少了它 launchd 拉起的进程会转头再托管一次自己——无限套娃。
	// 语义上也正确：launchd 要求被托管的服务进程自身不得 fork 到后台。
	args := []string{
		"--profile", Profile,
		"daemon",
		"--foreground",
		"--gateway-server-url", runConfig.ServerURL,
		"--name", runConfig.RuntimeName,
		"--work-dir", runConfig.WorkDir,
		"--max-run-duration", runConfig.MaxRunDuration.String(),
	}

	return launchd.Config{
		BinaryPath: binaryPath,
		Args:       args,
		Env:        env,
		WorkingDir: home,
		LogPath:    logPath,
	}, nil
}

func runDaemonStop() error {
	if err := ensureLaunchdSupported("stop"); err != nil {
		return err
	}
	installed, err := launchdStop()
	if err != nil {
		return err
	}
	if !installed {
		fmt.Println("daemon 未托管(没有 LaunchAgent),无需停止")
		return nil
	}
	fmt.Println("daemon 已停止(LaunchAgent 保留,登录不会自启)")
	fmt.Println("重新拉起: makecli daemon start;彻底移除: makecli daemon uninstall")
	return nil
}

func runDaemonUninstall() error {
	if err := ensureLaunchdSupported("uninstall"); err != nil {
		return err
	}
	installed, err := launchdUninstall()
	if err != nil {
		return err
	}
	nodeKeyRemoved, err := removeDaemonNodeKey()
	if err != nil {
		if installed {
			return fmt.Errorf("LaunchAgent 已移除，但删除当前 profile 的 node key 失败: %w", err)
		}
		return fmt.Errorf("删除当前 profile 的 node key 失败: %w", err)
	}
	if !installed && !nodeKeyRemoved {
		fmt.Println("daemon 未托管(没有 LaunchAgent),无需移除")
		return nil
	}
	if installed {
		fmt.Println("daemon 已停止,LaunchAgent 已移除")
	} else {
		fmt.Println("daemon 未托管(没有 LaunchAgent)")
	}
	if nodeKeyRemoved {
		fmt.Printf("%-10s %s\n", "已移除", fmt.Sprintf("credentials profile %q 里的 node key", Profile))
	}
	if logPath, err := daemonLogPath(); err == nil {
		fmt.Printf("%-10s %s\n", "保留", logPath+"(日志)")
	}
	fmt.Printf("%-10s %s\n", "保留", "~/.make/agent/work(工作目录)、credentials 里的其他凭证")
	fmt.Println("重新接入需使用新的 setup-key")
	return nil
}

// removeDaemonNodeKey 只删除当前 profile 的 runtime 身份，保留 access token
// 与其他 profile。无 key 时幂等返回 false，避免凭空创建 credentials 文件。
func removeDaemonNodeKey() (bool, error) {
	creds, err := config.Load()
	if err != nil {
		return false, err
	}
	profile, ok := creds[Profile]
	if !ok || profile.NodeKey == "" {
		return false, nil
	}
	profile.NodeKey = ""
	creds[Profile] = profile
	if err := config.Save(creds); err != nil {
		return false, err
	}
	return true, nil
}

func runDaemonRestart() error {
	if err := ensureLaunchdSupported("restart"); err != nil {
		return err
	}
	if err := launchdRestart(); err != nil {
		if errors.Is(err, launchd.ErrNotInstalled) {
			return fmt.Errorf("daemon 未托管,无从重启；先运行 `makecli daemon start`")
		}
		return err
	}
	fmt.Println("daemon 已重启(沿用 start 时敲定的参数)")
	return nil
}

func runDaemonStatus() error {
	if err := ensureLaunchdSupported("status"); err != nil {
		return err
	}
	output, err := resolveOutputFormat(daemonStatusOutput)
	if err != nil {
		return err
	}
	status, err := launchdQuery()
	if err != nil {
		return err
	}
	result := daemonStatusResult{
		Label:     status.Label,
		State:     daemonState(status),
		Disabled:  status.Disabled,
		PID:       status.PID,
		PlistPath: status.PlistPath,
		Command:   status.Command,
		LogPath:   status.LogPath,
	}
	if status.HasLastExit {
		lastExit := status.LastExitStatus
		result.LastExitStatus = &lastExit
	}
	if output == outputJSON {
		result.State = strings.ReplaceAll(result.State, " ", "_")
		return writeJSON(result)
	}
	renderDaemonStatus(result, status.Installed)
	return nil
}

func daemonState(status launchd.Status) string {
	switch {
	case !status.Installed:
		return daemonStateNotInstalled
	case status.Running:
		return daemonStateRunning
	default:
		return daemonStateStopped
	}
}

// renderDaemonStatus 走 key-value 平铺（cmd 渲染约定：头部信息不进表格）。
func renderDaemonStatus(result daemonStatusResult, installed bool) {
	fmt.Printf("%-10s %s\n", "Label", result.Label)
	fmt.Printf("%-10s %s\n", "State", result.State)
	if installed {
		// 「现在活没活」与「登录会不会自己回来」是两件事，分开报。
		autostart := "enabled"
		if result.Disabled {
			autostart = "disabled"
		}
		fmt.Printf("%-10s %s\n", "Autostart", autostart)
	}
	if result.PID > 0 {
		fmt.Printf("%-10s %d\n", "PID", result.PID)
	}
	if result.LastExitStatus != nil {
		fmt.Printf("%-10s %d\n", "Last exit", *result.LastExitStatus)
	}
	fmt.Printf("%-10s %s\n", "Plist", result.PlistPath)
	if len(result.Command) > 0 {
		fmt.Printf("%-10s %s\n", "Command", strings.Join(result.Command, " "))
	}
	if result.LogPath != "" {
		fmt.Printf("%-10s %s\n", "Logs", result.LogPath)
	}
	switch {
	case !installed:
		fmt.Println("未托管:运行 `makecli daemon start` 交给 launchd 常驻")
	case result.State == daemonStateStopped && result.Disabled:
		fmt.Println("已被 `makecli daemon stop` 停用:`makecli daemon start` 重新拉起")
	case result.State == daemonStateStopped:
		fmt.Println("已托管但当前没有进程在跑:看日志定位,或 `makecli daemon restart` 重拉")
	}
}

func init() {
	daemonStatusCmd.Flags().StringVarP(&daemonStatusOutput, "output", "o", outputAuto, "输出格式(auto|table|json)，auto 在终端为 table、管道/agent 下为 json")
	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonRestartCmd, daemonStatusCmd, daemonUninstallCmd)
}
