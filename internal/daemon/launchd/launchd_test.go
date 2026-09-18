/**
 * [INPUT]: 依赖 errors、os、path/filepath、strings、testing；打桩 runLaunchctl 隔离 launchctl 与 HOME 隔离文件系统
 * [OUTPUT]: 对外提供 launchd 托管原语的回归——plist 渲染/转义/读回、Install/Uninstall/Restart 的 launchctl 调用序、Query 状态判定
 * [POS]: internal/daemon/launchd 的测试面——托管逻辑全部落在纯函数与可打桩出口上，非 macOS 机器也能跑
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package launchd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubLaunchctl 打桩 launchctl 出口并记录调用序，返回调用记录指针。
func stubLaunchctl(t *testing.T, handler func(args ...string) (string, error)) *[][]string {
	t.Helper()
	original := runLaunchctl
	calls := &[][]string{}
	runLaunchctl = func(args ...string) (string, error) {
		*calls = append(*calls, args)
		if handler == nil {
			return "", nil
		}
		return handler(args...)
	}
	t.Cleanup(func() { runLaunchctl = original })
	return calls
}

// isolateHome 把 HOME 指向临时目录——plist 与日志都落在 home 下。
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func testConfig(home string) Config {
	return Config{
		BinaryPath: "/usr/local/bin/makecli",
		Args:       []string{"--profile", "default", "daemon", "--name", "mac & co"},
		Env:        map[string]string{"PATH": "/usr/local/bin:/usr/bin", "HOME": home},
		WorkingDir: home,
		LogPath:    filepath.Join(home, ".make", "agent", "logs", "daemon.log"),
	}
}

func TestRenderEscapesAndRoundTrips(t *testing.T) {
	home := isolateHome(t)
	config := testConfig(home)

	document, err := Render(config)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(document, "mac &amp; co") {
		t.Fatalf("参数里的 & 必须转义，实际:\n%s", document)
	}

	values, arrays, err := parsePlist([]byte(document))
	if err != nil {
		t.Fatalf("parsePlist: %v", err)
	}
	want := append([]string{config.BinaryPath}, config.Args...)
	got := arrays["ProgramArguments"]
	if len(got) != len(want) {
		t.Fatalf("ProgramArguments = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ProgramArguments[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if values["Label"] != Label {
		t.Fatalf("Label = %q, want %q", values["Label"], Label)
	}
	if values["StandardOutPath"] != config.LogPath || values["StandardErrorPath"] != config.LogPath {
		t.Fatalf("stdout/stderr 都应指向 %q, 实际 %q / %q", config.LogPath, values["StandardOutPath"], values["StandardErrorPath"])
	}
	// EnvironmentVariables 是嵌套 dict，其内层键不得污染顶层键值。
	if _, leaked := values["PATH"]; leaked {
		t.Fatal("嵌套 dict 的键泄漏到了顶层解析结果")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	home := isolateHome(t)
	config := testConfig(home)
	config.Env["MAKE_CLI_CONFIG_DIR"] = filepath.Join(home, "cfg")

	first, err := Render(config)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for range 5 {
		next, err := Render(config)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if next != first {
			t.Fatal("同一 Config 必须渲染出同样的字节（环境变量按键名排序）")
		}
	}
	if !strings.Contains(first, "<key>PATH</key>") {
		t.Fatalf("PATH 必须写进 plist:\n%s", first)
	}
}

func TestInstallWritesPlistAndBootstraps(t *testing.T) {
	home := isolateHome(t)
	calls := stubLaunchctl(t, nil)
	config := testConfig(home)

	plistPath, err := Install(config)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	if plistPath != want {
		t.Fatalf("plist 路径 = %q, want %q", plistPath, want)
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("plist 未落盘: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(config.LogPath)); err != nil {
		t.Fatalf("日志目录未创建: %v", err)
	}

	verbs := launchctlVerbs(*calls)
	// enable 打头：抵消 Stop 可能留下的 disable 记录，否则装完起不来。
	wantVerbs := []string{"enable", "bootout", "bootstrap", "kickstart"}
	if strings.Join(verbs, ",") != strings.Join(wantVerbs, ",") {
		t.Fatalf("launchctl 调用序 = %v, want %v", verbs, wantVerbs)
	}
	bootstrapCall := (*calls)[2]
	if bootstrapCall[len(bootstrapCall)-1] != plistPath {
		t.Fatalf("bootstrap 调用形状不对: %v", bootstrapCall)
	}
}

// launchctlVerbs 抽出调用序里的动词，便于断言顺序。
func launchctlVerbs(calls [][]string) []string {
	verbs := make([]string, 0, len(calls))
	for _, call := range calls {
		verbs = append(verbs, call[0])
	}
	return verbs
}

func TestInstallSurfacesBootstrapFailure(t *testing.T) {
	home := isolateHome(t)
	noBootstrapDelay(t)
	calls := stubLaunchctl(t, func(args ...string) (string, error) {
		if args[0] == "bootstrap" {
			return "Load failed: 5: Input/output error", errors.New("exit status 5")
		}
		return "", nil
	})

	if _, err := Install(testConfig(home)); err == nil {
		t.Fatal("bootstrap 失败必须上抛")
	} else if !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("错误应带上 launchctl 原文，实际: %v", err)
	}

	bootstraps := 0
	for _, call := range *calls {
		if call[0] == "bootstrap" {
			bootstraps++
		}
	}
	if bootstraps != bootstrapAttempts {
		t.Fatalf("bootstrap 应重试 %d 次(兜 launchd 异步卸载竞态)，实际 %d 次", bootstrapAttempts, bootstraps)
	}
	for _, call := range *calls {
		if call[0] == "kickstart" {
			t.Fatal("bootstrap 未成功时不得继续 kickstart")
		}
	}
}

func TestBootstrapRetrySucceedsAfterTransientFailure(t *testing.T) {
	home := isolateHome(t)
	noBootstrapDelay(t)
	attempts := 0
	stubLaunchctl(t, func(args ...string) (string, error) {
		if args[0] != "bootstrap" {
			return "", nil
		}
		attempts++
		if attempts == 1 {
			// bootout 是异步的：紧跟的第一次 bootstrap 常撞上"上一个还没退干净"。
			return "Bootstrap failed: 5: Input/output error", errors.New("exit status 5")
		}
		return "", nil
	})

	if _, err := Install(testConfig(home)); err != nil {
		t.Fatalf("首次 bootstrap 失败应自愈，实际: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("应在第二次成功即停，实际重试 %d 次", attempts)
	}
}

// noBootstrapDelay 去掉重试退避，避免测试真睡。
func noBootstrapDelay(t *testing.T) {
	t.Helper()
	original := bootstrapRetryDelay
	bootstrapRetryDelay = 0
	t.Cleanup(func() { bootstrapRetryDelay = original })
}

func TestStopKeepsPlistAndDisables(t *testing.T) {
	home := isolateHome(t)
	stubLaunchctl(t, nil)
	plistPath, err := Install(testConfig(home))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	calls := stubLaunchctl(t, nil)
	installed, err := Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !installed {
		t.Fatal("此前已托管，应返回 true")
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("stop 保留 plist（配置留着，uninstall 才删）: %v", err)
	}
	// disable 不可省：只 bootout 的话下次登录 launchd 又会把它拉起来。
	verbs := launchctlVerbs(*calls)
	want := []string{"bootout", "disable"}
	if strings.Join(verbs, ",") != strings.Join(want, ",") {
		t.Fatalf("launchctl 调用序 = %v, want %v", verbs, want)
	}
}

func TestStopSurfacesDisableFailure(t *testing.T) {
	home := isolateHome(t)
	stubLaunchctl(t, nil)
	if _, err := Install(testConfig(home)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	stubLaunchctl(t, func(args ...string) (string, error) {
		if args[0] == "disable" {
			return "Could not disable service", errors.New("exit status 1")
		}
		return "", nil
	})

	if _, err := Stop(); err == nil {
		t.Fatal("disable 失败必须上抛——用户会以为停干净了，登录后却自己回来")
	}
}

func TestStopWhenNotInstalled(t *testing.T) {
	isolateHome(t)
	stubLaunchctl(t, nil)

	installed, err := Stop()
	if err != nil {
		t.Fatalf("未托管时 stop 应幂等成功: %v", err)
	}
	if installed {
		t.Fatal("未托管应返回 false")
	}
}

func TestUninstallRemovesPlistAndClearsOverride(t *testing.T) {
	home := isolateHome(t)
	stubLaunchctl(t, nil)
	plistPath, err := Install(testConfig(home))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	calls := stubLaunchctl(t, nil)
	installed, err := Uninstall()
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !installed {
		t.Fatal("此前已托管，应返回 true")
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("uninstall 必须删除 plist: %v", err)
	}
	// enable 收尾：disable 记录以 Label 为键持久存在，留着会让下次安装静默起不来。
	verbs := launchctlVerbs(*calls)
	want := []string{"bootout", "enable"}
	if strings.Join(verbs, ",") != strings.Join(want, ",") {
		t.Fatalf("launchctl 调用序 = %v, want %v", verbs, want)
	}
}

func TestUninstallAfterStopIsClean(t *testing.T) {
	home := isolateHome(t)
	stubLaunchctl(t, nil)
	plistPath, err := Install(testConfig(home))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	installed, err := Uninstall()
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !installed {
		t.Fatal("stop 之后 plist 仍在，uninstall 应报告此前已托管")
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist 必须删除: %v", err)
	}
}

func TestUninstallWhenNotInstalled(t *testing.T) {
	isolateHome(t)
	calls := stubLaunchctl(t, nil)

	installed, err := Uninstall()
	if err != nil {
		t.Fatalf("未托管时 uninstall 应幂等成功: %v", err)
	}
	if installed {
		t.Fatal("未托管应返回 false")
	}
	// 即便 plist 早已不在，也要把可能残留的 disable 记录清掉。
	verbs := launchctlVerbs(*calls)
	if strings.Join(verbs, ",") != "bootout,enable" {
		t.Fatalf("launchctl 调用序 = %v", verbs)
	}
}

func TestRestartRequiresInstalled(t *testing.T) {
	isolateHome(t)
	stubLaunchctl(t, nil)

	if err := Restart(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("未托管时 restart 应返回 ErrNotInstalled，实际: %v", err)
	}
}

func TestRestartReusesExistingPlist(t *testing.T) {
	home := isolateHome(t)
	stubLaunchctl(t, nil)
	plistPath, err := Install(testConfig(home))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	before, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("读 plist: %v", err)
	}

	calls := stubLaunchctl(t, nil)
	if err := Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	after, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("读 plist: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("restart 不得重渲染 plist——start 时敲定的参数须原样保留")
	}
	verbs := launchctlVerbs(*calls)
	// enable 同样打头：stop 停用过的服务，restart 也要能重新拉起来。
	if strings.Join(verbs, ",") != "enable,bootout,bootstrap,kickstart" {
		t.Fatalf("restart 的 launchctl 调用序 = %v", verbs)
	}
}

const launchctlListRunning = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "cn.qfei.makecli.daemon";
	"OnDemand" = false;
	"LastExitStatus" = 0;
	"PID" = 4242;
	"Program" = "/usr/local/bin/makecli";
};`

const launchctlListStopped = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "cn.qfei.makecli.daemon";
	"OnDemand" = false;
	"LastExitStatus" = 1;
};`

func TestQueryRunning(t *testing.T) {
	home := isolateHome(t)
	stubLaunchctl(t, nil)
	config := testConfig(home)
	if _, err := Install(config); err != nil {
		t.Fatalf("Install: %v", err)
	}
	stubLaunchctl(t, func(args ...string) (string, error) {
		if args[0] == "list" {
			return launchctlListRunning, nil
		}
		return "", nil
	})

	status, err := Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !status.Installed || !status.Loaded || !status.Running {
		t.Fatalf("应为已托管且运行中: %+v", status)
	}
	if status.PID != 4242 {
		t.Fatalf("PID = %d, want 4242", status.PID)
	}
	if !status.HasLastExit || status.LastExitStatus != 0 {
		t.Fatalf("LastExitStatus 应解析为 0: %+v", status)
	}
	if len(status.Command) == 0 || status.Command[0] != config.BinaryPath {
		t.Fatalf("Command 应来自 plist 的 ProgramArguments: %v", status.Command)
	}
	if status.LogPath != config.LogPath {
		t.Fatalf("LogPath = %q, want %q", status.LogPath, config.LogPath)
	}
}

func TestQueryStoppedButInstalled(t *testing.T) {
	home := isolateHome(t)
	stubLaunchctl(t, nil)
	if _, err := Install(testConfig(home)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	stubLaunchctl(t, func(args ...string) (string, error) {
		return launchctlListStopped, nil
	})

	status, err := Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !status.Installed || !status.Loaded {
		t.Fatalf("应为已托管且已加载: %+v", status)
	}
	if status.Running || status.PID != 0 {
		t.Fatalf("无 PID 即未在跑: %+v", status)
	}
	if !status.HasLastExit || status.LastExitStatus != 1 {
		t.Fatalf("应带上最近退出码 1: %+v", status)
	}
}

func TestQueryReportsDisabled(t *testing.T) {
	home := isolateHome(t)
	stubLaunchctl(t, nil)
	if _, err := Install(testConfig(home)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// 两种 macOS 印法都要认（=> true 与 => disabled）。
	for _, printed := range []string{
		"disabled services = {\n\t\"" + Label + "\" => true\n}",
		"disabled services = {\n\t\"" + Label + "\" => disabled\n}",
	} {
		stubLaunchctl(t, func(args ...string) (string, error) {
			if args[0] == "print-disabled" {
				return printed, nil
			}
			return launchctlListStopped, nil
		})
		status, err := Query()
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if !status.Disabled {
			t.Fatalf("应识别为已停用，实际 %+v（launchctl 输出: %s）", status, printed)
		}
	}

	// 别的服务被禁用不该误伤自己。
	stubLaunchctl(t, func(args ...string) (string, error) {
		if args[0] == "print-disabled" {
			return "disabled services = {\n\t\"com.other.agent\" => true\n}", nil
		}
		return launchctlListRunning, nil
	})
	status, err := Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if status.Disabled {
		t.Fatal("只有自己这条 Label 的记录才算停用")
	}
}

func TestQueryNotInstalled(t *testing.T) {
	isolateHome(t)
	stubLaunchctl(t, func(args ...string) (string, error) {
		return `Could not find service "cn.qfei.makecli.daemon"`, errors.New("exit status 113")
	})

	status, err := Query()
	if err != nil {
		t.Fatalf("未托管不是故障，不该报错: %v", err)
	}
	if status.Installed || status.Loaded || status.Running {
		t.Fatalf("应为未托管零值: %+v", status)
	}
	if status.PlistPath == "" {
		t.Fatal("即便未托管也要给出 plist 期望路径")
	}
}
