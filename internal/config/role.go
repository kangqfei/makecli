/**
 * [INPUT]: 无外部依赖
 * [OUTPUT]: 对外提供 RoleUser 常量与 RoleNames 函数
 * [POS]: internal/config 的角色域常量（域取值单一真相源，与 channel.go / context.go 同责）——role 是这台机器上的人的 persona，
 *        与 context（连哪套后端）、profile（哪份凭证）正交；[settings] role 未设置 = 原有行为（skills 全量），
 *        目前唯一合法值 user（只用 makecli 管资源、不写 app，skills 只装 makecli），由 internal/skillsync RoleSkills 消费
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

// RoleUser：只用 makecli 管资源、不写 app。未设置 role 即全量（不另立 developer 值）。
const RoleUser = "user"

// RoleNames 返回全部合法角色名（供校验与错误提示）
func RoleNames() []string {
	return []string{RoleUser}
}
