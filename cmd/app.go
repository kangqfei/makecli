/**
 * [INPUT]: 依赖 github.com/spf13/cobra、fmt、path/filepath、regexp、slices
 * [OUTPUT]: 对外提供 newAppCmd 函数、loadAppManifestFromFile helper、validResourceKey 通用 key 校验函数
 * [POS]: cmd 模块的 app 命令组，挂载 create / list / info / init / delete / deploy / clone / pull（均 Hidden）/ promote 子命令；提供从 YAML 加载 App 清单的共享逻辑；validResourceKey 通用于 App / Entity / Field / Relation 的 key 格式校验
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/spf13/cobra"
)

// validKey 仅允许英文字母、数字、下划线，长度 2-20（不可以下划线开头）
var validKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]{1,19}$`)

// validResourceKey 校验资源 key 格式（适用于 App / Entity / Field / Relation）
// 规则: 英文字母、数字、下划线，长度 2-20，不能以下划线开头
func validResourceKey(key string) error {
	if !validKey.MatchString(key) {
		return fmt.Errorf("invalid key %q: 仅支持英文字母、数字、下划线，长度 2-20，不能以下划线开头", key)
	}
	return nil
}

func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Manage apps",
	}
	cmd.AddCommand(newAppCreateCmd())
	cmd.AddCommand(newAppInitCmd())
	cmd.AddCommand(newAppListCmd())
	cmd.AddCommand(newAppInfoCmd())
	cmd.AddCommand(newAppDeleteCmd())
	cmd.AddCommand(newDeployCmd())
	cmd.AddCommand(newCloneCmd())
	cmd.AddCommand(newPullCmd())
	cmd.AddCommand(newPromoteCmd())
	return cmd
}

// loadAppManifestFromFile 从 YAML 文件加载唯一的 Make.App 资源清单
func loadAppManifestFromFile(path string) (ResourceManifest, error) {
	ext := filepath.Ext(path)
	if !slices.Contains(recognizedManifestExtensions, ext) {
		return ResourceManifest{}, fmt.Errorf("文件必须为 .yaml 或 .yml 格式")
	}

	manifests, err := loadManifestsFromFile(path)
	if err != nil {
		return ResourceManifest{}, err
	}

	var apps []ResourceManifest
	for _, m := range manifests {
		if m.Type == "Make.App" {
			apps = append(apps, m)
		}
	}

	if len(apps) == 0 {
		return ResourceManifest{}, fmt.Errorf("文件中未找到 Make.App 资源")
	}
	if len(apps) > 1 {
		return ResourceManifest{}, fmt.Errorf("文件中包含多个 Make.App 资源，期望恰好一个")
	}

	return apps[0], nil
}
