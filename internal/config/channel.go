/**
 * [INPUT]: 无外部依赖
 * [OUTPUT]: 对外提供 ChannelStable/ChannelBeta/DefaultChannel 常量与 ChannelNames 函数
 * [POS]: internal/config 的发布通道域常量（域取值单一真相源，与 context.go 同责），被 cmd 层与 internal/notifier 消费
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

// 发布通道：stable 只跟踪正式版（GitHub /releases/latest 服务端语义），
// beta 额外跟踪 prerelease（/releases 列表取 semver 最高，候选天然含稳定版）。
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"

	// DefaultChannel 是未配置时的回退通道
	DefaultChannel = ChannelStable
)

// ChannelNames 返回全部合法通道名（固定顺序，供校验与错误提示）
func ChannelNames() []string {
	return []string{ChannelStable, ChannelBeta}
}
