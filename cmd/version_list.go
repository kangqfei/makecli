/**
 * [INPUT]: 依赖 cmd/output（writeJSON / resolveOutputFormat / outputTable / outputJSON）、internal/update（ListReleases）、internal/build（Version）、fmt、os、strings、github.com/olekukonko/tablewriter、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newVersionListCmd 函数（包内）
 * [POS]: cmd 模块 version 子命令下的 list 子命令，列出 GitHub 上的历史 release（TYPE 列标注 Pre-release，对齐 gh release list）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/build"
	"github.com/qfeius/makecli/internal/update"
	"github.com/spf13/cobra"
)

func newVersionListCmd() *cobra.Command {
	var limit int
	var output string
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List historical releases from GitHub",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVersionList(limit, output)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "number of releases to fetch (1-100)")
	addOutputFlag(cmd, &output)
	return cmd
}

func runVersionList(limit int, output string) error {
	if limit < 1 || limit > 100 {
		return fmt.Errorf("limit must be between 1 and 100")
	}
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}

	releases, err := update.ListReleases(limit)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(toReleaseJSONView(releases))
	}
	return renderReleaseTable(releases, build.Version)
}

type releaseJSONView struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	HTMLURL     string `json:"html_url"`
}

func toReleaseJSONView(releases []update.Release) []releaseJSONView {
	out := make([]releaseJSONView, len(releases))
	for i, r := range releases {
		out[i] = releaseJSONView{
			TagName:     r.TagName,
			Name:        r.Name,
			PublishedAt: r.PublishedAt,
			Prerelease:  r.Prerelease,
			HTMLURL:     r.HTMLURL,
		}
	}
	return out
}

func renderReleaseTable(releases []update.Release, currentVersion string) error {
	if len(releases) == 0 {
		fmt.Println("No releases found.")
		return nil
	}

	currentTag := strings.TrimPrefix(currentVersion, "v")
	rows := make([][]string, len(releases))
	for i, r := range releases {
		marker := ""
		if strings.TrimPrefix(r.TagName, "v") == currentTag && currentTag != "" && currentTag != "DEV" {
			marker = "*"
		}
		rows[i] = []string{marker, r.TagName, releaseTypeLabel(r.Prerelease), r.PublishedAt, r.HTMLURL}
	}

	table := tablewriter.NewTable(os.Stdout)
	table.Header("CURRENT", "VERSION", "TYPE", "PUBLISHED", "URL")
	_ = table.Bulk(rows)
	_ = table.Render()
	return nil
}

// releaseTypeLabel 对齐 gh release list 的 TYPE 列：预发布标注，正式版留空
func releaseTypeLabel(prerelease bool) string {
	if prerelease {
		return "Pre-release"
	}
	return ""
}
