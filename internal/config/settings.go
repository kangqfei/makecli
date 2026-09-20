/**
 * [INPUT]: 依赖 fmt、os、regexp、strconv；依赖 config.go 的 parseINISections、ConfigPath
 * [OUTPUT]: 对外提供 Settings 类型、LoadSettings、ValidateProfileName 函数；包内 settingsSection 常量、validProfileName 正则、legacySettingKeys 表
 * [POS]: internal/config 的全局设置读取，承载非 profile 相关的 [settings] 段（check-for-updates / context / channel）；
 *        旧键（environment）只登记进 Settings.Legacy 不翻译——解析链不背历史包袱，迁移由 MigrateSettings（doctor）一次性完成；
 *        ValidateProfileName 同时承担 profile 名文法把关（保守文法 + 保留名），是所有写路径的 INI 注入第一道闸
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// settingsSection 是 config 文件中承载全局（非 profile）配置的保留段名
const settingsSection = "settings"

// validProfileName 是 profile 名的保守文法：字母数字开头，后接字母数字或 . _ -，总长 1-64。
// profile 名会被原样写进 INI 的 [section] 头——空名、括号、换行都能伪造/截断 section 边界
// （如 "evil]\n[other" 会注入一个新段），故文法必须收紧到不可能触碰 INI 语法字符。
var validProfileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateProfileName 校验 profile 名：先过保守文法（防 INI section 注入），再拒绝保留段名
// settings。否则写出的 [settings] profile 段会与全局 [settings] 段在同一 INI 文件里碰撞——
// 读时被 profile 解析层跳过、数据静默丢失。profile 与 settings 共用 INI section 命名空间，故此名必须独占。
func ValidateProfileName(name string) error {
	if !validProfileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: must start with a letter or digit and contain only letters, digits, '.', '_' or '-' (max 64 characters)", name)
	}
	if name == settingsSection {
		return fmt.Errorf("%q is a reserved section name and cannot be used as a profile", name)
	}
	return nil
}

// legacySettingKeys 是已改名的 [settings] 键：旧键 → 新键。
// 读路径只登记命中（Settings.Legacy），不做翻译；MigrateSettings 按此表搬家。
// 新增改名只需加一行，doctor 与 LoadSettings 自动跟上。
var legacySettingKeys = map[string]string{
	"environment": "context",
}

// Settings 持有全局配置项。指针字段表达三态：nil = 文件未设置该项。
type Settings struct {
	// CheckForUpdates 控制自动更新提示是否启用；nil 表示未配置（由调用方决定默认）
	CheckForUpdates *bool
	// Context 是当前后端 context 名（dev/test/production）；空串 = 未配置（调用方回退 DefaultContext）
	Context string
	// Channel 是发布通道名（stable/beta）；空串 = 未配置（调用方回退 DefaultChannel）
	Channel string
	// Legacy 是文件里仍在用旧名的键（旧键 → 值）；非空即配置过期，调用方应指引 makecli doctor --fix
	Legacy map[string]string
}

// LoadSettings 读取 config 文件的 [settings] 全局段。
// best-effort：文件不存在返回空 Settings 且无错误；解析失败返回错误。
func LoadSettings() (Settings, error) {
	path, err := ConfigPath()
	if err != nil {
		return Settings{}, err
	}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("读取 config 失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	sections, err := parseINISections(f)
	if err != nil {
		return Settings{}, err
	}

	var s Settings
	if kv, ok := sections[settingsSection]; ok {
		if raw, ok := kv["check-for-updates"]; ok {
			if b, err := strconv.ParseBool(raw); err == nil {
				s.CheckForUpdates = &b
			}
		}
		s.Context = kv["context"]
		s.Channel = kv["channel"]
		for old := range legacySettingKeys {
			if v, ok := kv[old]; ok {
				if s.Legacy == nil {
					s.Legacy = map[string]string{}
				}
				s.Legacy[old] = v
			}
		}
	}
	return s, nil
}
