/**
 * [INPUT]: 依赖 protocol.go 的 Block/MentionTarget 与标准库 regexp；语义真相源 agent-design/docs/channels.md §7.5（互@平台内直通）
 * [OUTPUT]: 对外（包内）提供 parseMentionBlocks——把 CLI 最终答复文本切成 text + mention 块序列
 * [POS]: internal/daemon 的出站 mention 解析——LLM 以 @Name 表达互@，此处是"文本 → 结构化 mention 块"的唯一物化点
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package daemon

import "regexp"

// mentionPattern 匹配 @Name 语法：名字是 ASCII 单词字符（可含 - 连接），
// 与平台 agent 命名域对齐。邮箱等 "x@y" 形态靠前置边界排除——@ 前必须是
// 行首或非单词字符。
var mentionPattern = regexp.MustCompile(`(^|[^A-Za-z0-9_@])@([A-Za-z0-9_][A-Za-z0-9_-]*)`)

// parseMentionBlocks 把最终答复文本切成 text + mention 块序列。mention 的
// Target 先以名字寻址（Kind=agent, ID=名字）——平台按 id-or-name 解析并归
// 一化为规范 agent_id，未命中的 mention 在渠道侧退化为 @名字 文本，零副
// 作用；因此这里宁可多切（把普通 @词 也切成 mention），不做本地名册校验。
func parseMentionBlocks(text string) []Block {
	matches := mentionPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return []Block{{Kind: "text", Text: text}}
	}
	var blocks []Block
	cursor := 0
	for _, match := range matches {
		// match: [全匹配起止, 边界组起止, 名字组起止]——文本切分点在名字组
		// 的 @ 处（边界字符归前一个 text 块）。
		atStart := match[4] - 1
		name := text[match[4]:match[5]]
		if leading := text[cursor:atStart]; leading != "" {
			blocks = append(blocks, Block{Kind: "text", Text: leading})
		}
		blocks = append(blocks, Block{
			Kind: "mention", Text: name,
			Target: &MentionTarget{Kind: "agent", ID: name},
		})
		cursor = match[5]
	}
	if trailing := text[cursor:]; trailing != "" {
		blocks = append(blocks, Block{Kind: "text", Text: trailing})
	}
	return blocks
}
