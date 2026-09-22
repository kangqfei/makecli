/**
 * [INPUT]: 依赖 os、bufio、fmt、io、regexp、sort、strings、path/filepath；依赖 paths.go 的 Dir、settings.go 的 ValidateProfileName
 * [OUTPUT]: 对外提供 LoadConfig、SaveConfig、SetSetting、UnsetSetting、MigrateSettings、ConfigPath 函数，Config/ConfigProfile 类型；包内 updateSettings（[settings] read-modify-write 原语）、validateINIKey / validateINIValue（写路径 INI 注入防线，被 credentials.go 复用）
 * [POS]: internal/config 的 config 文件管理，读写 config 文件（默认 ~/.make/config，INI 格式）；
 *        [settings] 的三种写法（SetSetting 单键、UnsetSetting 删键、MigrateSettings 旧键搬家）都经 updateSettings 一条路；
 *        所有落盘键值先过 validateINIKey/validateINIValue（拒换行与首尾空白，防止值注入伪造 section/键）
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ---------------------------------- INI 写入防线 ----------------------------------

// validINIKey 是允许落盘的 INI 键名文法（与 profile 名同族但不限长度上限之外的约束）。
// 键名会被原样写成 `key = value` 行，空白/括号/等号/换行都能破坏行结构或伪造新段。
var validINIKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// validateINIKey 校验将写入 INI 的键名，拒绝一切可能触碰 INI 语法的字符。
func validateINIKey(key string) error {
	if !validINIKey.MatchString(key) {
		return fmt.Errorf("非法配置键 %q: 仅支持字母数字开头，后接字母数字或 . _ -", key)
	}
	return nil
}

// validateINIValue 校验将写入 INI 的值：含换行的值会把后续内容注入成新的行/段
// （如 "x\n[evil]" 伪造 section），含首尾空白的值经读路径 TrimSpace 后无法原样读回。
// field 仅用于错误定位（指明是哪个 profile 的哪个键）。
func validateINIValue(field, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s 含换行符，会破坏 INI 文件结构，拒绝写入", field)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s 含前导/尾随空白，写回后无法原样读出，拒绝写入", field)
	}
	return nil
}

// ---------------------------------- 数据结构 ----------------------------------

// ConfigProfile 代表一个命名配置块，如 [default]，持有默认 context、服务器地址、租户与操作者信息
type ConfigProfile struct {
	Context       string
	MetaServerURL string
	RepoServerURL string
	AuthServerURL string
	XTenantID     string
	OperatorID    string
}

// Config 是所有 profile 的集合，key 为 profile 名
type Config map[string]ConfigProfile

// ---------------------------------- 路径 ----------------------------------

// ConfigPath 返回 config 文件的绝对路径
// 默认 ~/.make/config，被 $MAKE_CLI_CONFIG_DIR 覆盖
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config"), nil
}

// ---------------------------------- 读取 ----------------------------------

// LoadConfig 从 ~/.make/config 读取所有 profile
// 文件不存在时返回空 Config，不报错
func LoadConfig() (Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 config 失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	return parseConfigINI(f)
}

// parseINISections 通用 INI 解析：section → (key → value)。
// 忽略空行与 # / ; 注释；无 section 头的键被丢弃。
func parseINISections(r io.Reader) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	current := ""

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.TrimSpace(line[1 : len(line)-1])
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		if current == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		sections[current][strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return sections, scanner.Err()
}

// parseConfigINI 解析 config 文件为 Config（profile 集合），跳过保留的 [settings] 全局段
func parseConfigINI(f *os.File) (Config, error) {
	sections, err := parseINISections(f)
	if err != nil {
		return nil, err
	}
	cfg := Config{}
	for name, kv := range sections {
		if name == settingsSection {
			continue
		}
		cfg[name] = ConfigProfile{
			Context:       kv["context"],
			MetaServerURL: kv["meta-server-url"],
			RepoServerURL: kv["repo-server-url"],
			AuthServerURL: kv["auth-server-url"],
			XTenantID:     kv["X-Tenant-ID"],
			OperatorID:    kv["X-Operator-ID"],
		}
	}
	return cfg, nil
}

// ---------------------------------- 写入 ----------------------------------

// existingSettings 读取磁盘上已存在的 [settings] 段（Config 模型不含全局段，
// SaveConfig 覆盖写时需显式保留，否则用户手写的全局配置会丢失）。
func existingSettings(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	sections, err := parseINISections(f)
	if err != nil {
		return nil
	}
	return sections[settingsSection]
}

// SaveConfig 将 Config 写入 ~/.make/config，原样保留磁盘上已有的 [settings] 全局段。
// 自动创建 ~/.make/ 目录（0700），文件权限 0600
func SaveConfig(cfg Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return saveConfigWithSettings(cfg, existingSettings(path))
}

// SetSetting 写入 [settings] 段的单个全局键，保留其余内容。
func SetSetting(key, value string) error {
	return updateSettings(func(settings map[string]string) { settings[key] = value })
}

// UnsetSetting 删除 [settings] 段的单个全局键（不存在即 no-op），保留其余内容。
func UnsetSetting(key string) error {
	return updateSettings(func(settings map[string]string) { delete(settings, key) })
}

// MigrateSettings 把 [settings] 里仍用旧名的键按 legacySettingKeys 搬到新键：
// 新键已存在则以新键为准只删旧键（用户已手动迁移过），否则值随键搬家。
// 返回实际执行的搬家清单（旧键 → 新键），空清单 = 无事可做且不落盘。
func MigrateSettings() (map[string]string, error) {
	moved := map[string]string{}
	err := updateSettings(func(settings map[string]string) {
		for old, current := range legacySettingKeys {
			v, ok := settings[old]
			if !ok {
				continue
			}
			if _, exists := settings[current]; !exists {
				settings[current] = v
			}
			delete(settings, old)
			moved[old] = current
		}
	})
	return moved, err
}

// updateSettings 是 [settings] 段的 read-modify-write 原语：
// 读取现有 profile 段与 [settings]，交 mutate 就地修改后整体落盘，保留其余内容。
// mutate 未做任何改动时同样落盘（幂等重写，无副作用）。
func updateSettings(mutate func(settings map[string]string)) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	settings := existingSettings(path)
	if settings == nil {
		settings = map[string]string{}
	}
	mutate(settings)
	return saveConfigWithSettings(cfg, settings)
}

// saveConfigWithSettings 是 config 文件的唯一写路径：落盘 profile 段 + 显式的 [settings] 段。
// settings 由调用方提供（SaveConfig 传磁盘现状以保留，SetSetting 传修改后的副本）。
// 落盘前对所有 profile 名与键值过 INI 注入防线（文法 + 换行/首尾空白拒绝）。
func saveConfigWithSettings(cfg Config, settings map[string]string) error {
	for name, p := range cfg {
		if err := ValidateProfileName(name); err != nil {
			return err
		}
		for field, value := range map[string]string{
			"context":         p.Context,
			"meta-server-url": p.MetaServerURL,
			"repo-server-url": p.RepoServerURL,
			"auth-server-url": p.AuthServerURL,
			"X-Tenant-ID":     p.XTenantID,
			"X-Operator-ID":   p.OperatorID,
		} {
			if err := validateINIValue(fmt.Sprintf("profile %q 的 %s", name, field), value); err != nil {
				return err
			}
		}
	}
	for k, v := range settings {
		if err := validateINIKey(k); err != nil {
			return err
		}
		if err := validateINIValue(fmt.Sprintf("[settings] 的 %s", k), v); err != nil {
			return err
		}
	}

	path, err := ConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建配置目录 %s 失败: %w", dir, err)
	}

	// default profile 优先输出，其余保持稳定顺序
	order := []string{}
	if _, ok := cfg["default"]; ok {
		order = append(order, "default")
	}
	for name := range cfg {
		if name != "default" {
			order = append(order, name)
		}
	}

	if err := atomicWrite(path, 0600, func(w io.Writer) error {
		for i, name := range order {
			if i > 0 {
				_, _ = fmt.Fprintln(w)
			}
			_, _ = fmt.Fprintf(w, "[%s]\n", name)
			p := cfg[name]
			if p.Context != "" {
				_, _ = fmt.Fprintf(w, "context = %s\n", p.Context)
			}
			if p.MetaServerURL != "" {
				_, _ = fmt.Fprintf(w, "meta-server-url = %s\n", p.MetaServerURL)
			}
			if p.RepoServerURL != "" {
				_, _ = fmt.Fprintf(w, "repo-server-url = %s\n", p.RepoServerURL)
			}
			if p.AuthServerURL != "" {
				_, _ = fmt.Fprintf(w, "auth-server-url = %s\n", p.AuthServerURL)
			}
			if p.XTenantID != "" {
				_, _ = fmt.Fprintf(w, "X-Tenant-ID = %s\n", p.XTenantID)
			}
			if p.OperatorID != "" {
				_, _ = fmt.Fprintf(w, "X-Operator-ID = %s\n", p.OperatorID)
			}
		}

		// 末尾保留全局 [settings] 段（读路径跳过它，写路径必须显式回写，否则数据丢失）
		if len(settings) > 0 {
			if len(order) > 0 {
				_, _ = fmt.Fprintln(w)
			}
			_, _ = fmt.Fprintf(w, "[%s]\n", settingsSection)
			keys := make([]string, 0, len(settings))
			for k := range settings {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				_, _ = fmt.Fprintf(w, "%s = %s\n", k, settings[k])
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("写入 config 失败: %w", err)
	}
	return nil
}
