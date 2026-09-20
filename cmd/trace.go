/**
 * [INPUT]: 依赖 github.com/spf13/cobra、errors、fmt、net/url、time；复用 client.go 的 resolveContext（context preset）与 login.go 的 openBrowserFunc（浏览器打开桩点）
 * [OUTPUT]: 对外提供 traceCmd——`makecli trace` 子命令（Hidden：内部排障工具，不对普通用户展示）；包内 traceURL 纯函数、nowFunc 桩点
 * [POS]: cmd 模块的 trace 直达入口：按 --id 与时间窗口拼 OpenObserve trace-details URL 并打开本地浏览器；
 *        OpenObserve 基址随全局 --context 取自 config.Context.TraceServerURL（dev/test → openobserve.qtech.cn，production → openobserve.qfei.cn）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

var (
	traceID      string
	traceDays    int
	traceHours   int
	traceMinutes int
)

// nowFunc 为包级可打桩变量，单测冻结当前时间以断言 from/to。
var nowFunc = time.Now

// defaultTraceWindow 是未指定任何窗口 flag 时的回溯长度：trace 多在排障当下查看，10 分钟足够且页面加载快。
const defaultTraceWindow = 10 * time.Minute

var errTraceIDMissing = errors.New("--id is required")

// traceCmd 打开 OpenObserve 的 trace 直达页。
// Hidden：内部排障用，不在 help 中对普通用户展示。
var traceCmd = &cobra.Command{
	Use:          "trace",
	Short:        "在浏览器打开 OpenObserve 的 trace 详情页",
	Hidden:       true,
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if traceID == "" {
			return errTraceIDMissing
		}
		window, err := traceWindow(traceDays, traceHours, traceMinutes)
		if err != nil {
			return err
		}
		_, c, err := resolveContext()
		if err != nil {
			return err
		}
		target := traceURL(c.TraceServerURL, traceID, window, nowFunc())
		// URL 先落 stdout：浏览器打不开时用户仍可手动复制；agent 场景也能直接消费。
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), target)
		return openBrowserFunc(target)
	},
}

// traceWindow 把 days/hours/minutes 合成回溯窗口；全零回退默认 10 分钟，负值拒绝。
func traceWindow(days, hours, minutes int) (time.Duration, error) {
	if days < 0 || hours < 0 || minutes < 0 {
		return 0, errors.New("--days/--hours/--minutes must not be negative")
	}
	window := time.Duration(days)*24*time.Hour + time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute
	if window == 0 {
		window = defaultTraceWindow
	}
	return window, nil
}

// traceURL 拼 OpenObserve trace-details 直达 URL：from/to 为微秒级 Unix 时间戳，to=now，from=to-window。
func traceURL(base, id string, window time.Duration, now time.Time) string {
	to := now.UnixMicro()
	q := url.Values{}
	q.Set("org_identifier", "default")
	q.Set("stream", "default")
	q.Set("trace_id", id)
	q.Set("from", fmt.Sprint(to-window.Microseconds()))
	q.Set("to", fmt.Sprint(to))
	return base + "/web/traces/trace-details?" + q.Encode()
}

func init() {
	traceCmd.Flags().StringVarP(&traceID, "id", "i", "", "trace id(必填)")
	traceCmd.Flags().IntVarP(&traceDays, "days", "d", 0, "回溯窗口:天")
	traceCmd.Flags().IntVarP(&traceHours, "hours", "H", 0, "回溯窗口:小时")
	traceCmd.Flags().IntVarP(&traceMinutes, "minutes", "m", 0, "回溯窗口:分钟(三者相加,全零默认 10 分钟)")
	rootCmd.AddCommand(traceCmd)
}
