/**
 * [INPUT]: 依赖 trace.go 的 traceURL / traceWindow / traceCmd 与 openBrowserFunc / nowFunc 桩点；setEnvFlag 测试辅助；internal/config 隔离
 * [OUTPUT]: 对外提供 trace 子命令的单元测试
 * [POS]: cmd 模块 trace.go 的配套测试：锁定 URL 生成规则（微秒时间戳、from<to、默认 10 分钟窗口）与环境→OpenObserve 基址映射
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTraceCommandHiddenFromHelp(t *testing.T) {
	if !traceCmd.Hidden {
		t.Fatal("trace 应为 Hidden")
	}
}

func TestTraceURLMicrosecondWindow(t *testing.T) {
	now := time.UnixMicro(1789563655676458)
	got := traceURL("https://openobserve.qtech.cn", "7c197b964fca440fe24632ed2e503e6d", 24*time.Hour, now)
	want := "https://openobserve.qtech.cn/web/traces/trace-details?from=1789477255676458&org_identifier=default&stream=default&to=1789563655676458&trace_id=7c197b964fca440fe24632ed2e503e6d"
	if got != want {
		t.Fatalf("traceURL =\n%s\nwant\n%s", got, want)
	}
}

func TestTraceWindow(t *testing.T) {
	tests := []struct {
		days, hours, minutes int
		want                 time.Duration
		wantErr              bool
	}{
		{0, 0, 0, 10 * time.Minute, false},
		{1, 1, 15, 25*time.Hour + 15*time.Minute, false},
		{0, 0, 30, 30 * time.Minute, false},
		{-1, 0, 0, 0, true},
	}
	for _, tt := range tests {
		got, err := traceWindow(tt.days, tt.hours, tt.minutes)
		if (err != nil) != tt.wantErr {
			t.Fatalf("traceWindow(%d,%d,%d) err = %v", tt.days, tt.hours, tt.minutes, err)
		}
		if got != tt.want {
			t.Fatalf("traceWindow(%d,%d,%d) = %v, want %v", tt.days, tt.hours, tt.minutes, got, tt.want)
		}
	}
}

// setTraceFlags 临时覆盖 trace 子命令的包级 flag 变量，结束自动还原。
func setTraceFlags(t *testing.T, id string, days, hours, minutes int) {
	t.Helper()
	oldID, oldD, oldH, oldM := traceID, traceDays, traceHours, traceMinutes
	traceID, traceDays, traceHours, traceMinutes = id, days, hours, minutes
	t.Cleanup(func() { traceID, traceDays, traceHours, traceMinutes = oldID, oldD, oldH, oldM })
}

func TestTraceRequiresID(t *testing.T) {
	setTraceFlags(t, "", 0, 0, 0)
	if err := traceCmd.RunE(traceCmd, nil); !errors.Is(err, errTraceIDMissing) {
		t.Fatalf("err = %v, want errTraceIDMissing", err)
	}
}

func TestTraceOpensBrowserByEnvironment(t *testing.T) {
	t.Setenv("MAKE_CLI_CONFIG_DIR", t.TempDir())
	oldOpen, oldNow := openBrowserFunc, nowFunc
	t.Cleanup(func() { openBrowserFunc, nowFunc = oldOpen, oldNow })
	nowFunc = func() time.Time { return time.UnixMicro(1789563655676458) }

	tests := []struct {
		environment string
		wantHost    string
	}{
		{"dev", "openobserve.qtech.cn"},
		{"test", "openobserve.qtech.cn"},
		{"production", "openobserve.qfei.cn"},
	}
	for _, tt := range tests {
		var opened string
		openBrowserFunc = func(u string) error { opened = u; return nil }
		setEnvFlag(t, tt.environment)

		var out bytes.Buffer
		traceCmd.SetOut(&out)
		setTraceFlags(t, "abc123", 0, 1, 0)
		if err := traceCmd.RunE(traceCmd, nil); err != nil {
			t.Fatalf("%s: %v", tt.environment, err)
		}
		u, err := url.Parse(opened)
		if err != nil {
			t.Fatalf("%s: bad url %q: %v", tt.environment, opened, err)
		}
		if u.Host != tt.wantHost {
			t.Fatalf("%s: host = %q, want %q", tt.environment, u.Host, tt.wantHost)
		}
		q := u.Query()
		if q.Get("trace_id") != "abc123" || q.Get("to") != "1789563655676458" || q.Get("from") != "1789560055676458" {
			t.Fatalf("%s: query = %v", tt.environment, q)
		}
		if strings.TrimSpace(out.String()) != opened {
			t.Fatalf("%s: stdout 应回显 URL: %q", tt.environment, out.String())
		}
	}
}

func TestTraceShortFlags(t *testing.T) {
	// -h 被 help 占用，hours 用大写 -H（对齐 date/curl 惯例）。
	for long, short := range map[string]string{"id": "i", "days": "d", "hours": "H", "minutes": "m"} {
		f := traceCmd.Flags().Lookup(long)
		if f == nil || f.Shorthand != short {
			t.Errorf("--%s shorthand = %v, want -%s", long, f, short)
		}
	}
}
