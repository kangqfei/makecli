/**
 * [INPUT]: 依赖 internal/config（RoleUser）
 * [OUTPUT]: 对外提供 RoleSkills 函数（角色 → skills 名单）与 SyncCommand（角色 → 同步命令）
 * [POS]: internal/skillsync 的角色名单表：哪个角色装哪些 skills 是 CLI 的 UX 决策，故放这里而非 skills 仓库；
 *        user 只装 makecli（用 CLI 管资源不写 app），未设置 role 即全量（原有行为）；Sync（update 后置同步）与 cmd/skills install --role 共用此表，
 *        保证 update 不会把 user 的选择冲回全量
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package skillsync

import "github.com/qfeius/makecli/internal/config"

// roleSkills 是角色 → skills 名单。不在表里的角色（含未设置的空串）= 全量（走上游 --all）。
var roleSkills = map[string][]string{
	config.RoleUser: {"makecli"},
}

// RoleSkills 返回角色对应的安装名单：names 非空按名装，all 为真走全量。
// 空 / 未知角色一律全量——名单表不做校验，取值合法性由 cmd 层的 settings 键表把关。
func RoleSkills(role string) (names []string, all bool) {
	names = roleSkills[role]
	return names, names == nil
}

// SyncCommand 返回某角色的同步命令：未设置 role 即 SkillsCommand（--all，原有行为），user 走按名 InstallCommand。
func SyncCommand(role string) []string {
	return InstallCommand(RoleSkills(role))
}
