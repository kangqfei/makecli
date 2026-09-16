/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、cmd/output（writeJSON / resolveOutputFormat）、internal/api（OCROptions）、fmt、os、path/filepath、strings、github.com/olekukonko/tablewriter、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newIntegrationOCRCmd 函数
 * [POS]: cmd/integration 的 ocr 子命令，上传本地 PDF/PNG/JPG/OFD 给 OCR 服务并按 spec 渲染票据结构化结果
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

// ocrAllowedExtensions 限定可上传的文件后缀（小写比对）；spec 支持 PDF/OFD/图片
var ocrAllowedExtensions = map[string]bool{
	".pdf":  true,
	".ofd":  true,
	".png":  true,
	".jpg":  true,
	".jpeg": true,
}

func newIntegrationOCRCmd() *cobra.Command {
	var (
		file       string
		output     string
		businessID int64
		verifyVAT  bool

		coordRestoreOriginal    bool
		specificPages           string
		cropCompleteImage       bool
		cropValueImage          bool
		mergeDigitalElecInvoice bool
		returnPPI               bool
	)

	cmd := &cobra.Command{
		Use:          "ocr -f <file>",
		Short:        "Recognize bills from a PDF/OFD/PNG/JPG file",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			// 仅在用户显式设置 --verify-vat 时才发送，否则让服务端用默认值 (true)
			var verifyVATPtr *bool
			if c.Flags().Changed("verify-vat") {
				v := verifyVAT
				verifyVATPtr = &v
			}
			opts := api.OCROptions{
				BusinessID:              businessID,
				VerifyVAT:               verifyVATPtr,
				CoordRestoreOriginal:    coordRestoreOriginal,
				SpecificPages:           specificPages,
				CropCompleteImage:       cropCompleteImage,
				CropValueImage:          cropValueImage,
				MergeDigitalElecInvoice: mergeDigitalElecInvoice,
				ReturnPPI:               returnPPI,
			}
			return runIntegrationOCR(file, output, opts)
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "path to PDF/OFD/PNG/JPG file (required)")
	addOutputFlag(cmd, &output)
	cmd.Flags().Int64Var(&businessID, "business-id", 0, "business document ID (multipart business_id)")
	cmd.Flags().BoolVar(&verifyVAT, "verify-vat", true, "enable invoice authenticity verification (server default: true)")
	cmd.Flags().BoolVar(&coordRestoreOriginal, "coord-restore-original", false, "return coordinates against the original image (default: cropped image)")
	cmd.Flags().StringVar(&specificPages, "pages", "", `pages to recognize, e.g. "1,3,2" or "2-4"`)
	cmd.Flags().BoolVar(&cropCompleteImage, "crop-complete", false, "include base64 of each bill crop")
	cmd.Flags().BoolVar(&cropValueImage, "crop-value", false, "include base64 of each value crop")
	cmd.Flags().BoolVar(&mergeDigitalElecInvoice, "merge-elec", false, "merge multi-page digital electronic invoices into one")
	cmd.Flags().BoolVar(&returnPPI, "return-ppi", false, "return PDF decoding PPI")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func runIntegrationOCR(file, output string, opts api.OCROptions) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(file))
	if !ocrAllowedExtensions[ext] {
		return fmt.Errorf("unsupported file extension %q, valid: .pdf .ofd .png .jpg .jpeg", ext)
	}

	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	client, err := newClientFromProfile()
	if err != nil {
		return err
	}

	data, err := client.OCR(file, f, opts)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{"data": data})
	}
	return renderOCRTable(data)
}

// renderOCRTable 按 spec sample 结构 (data.file_name + data.result.pages[].bills[].items[]) 渲染
// 风格对齐 entity list <name>：顶部 key:value header + 每张票一个 LABEL/VALUE 边框表格
// 任一断言失败则回退 JSON 输出，避免被服务端结构变化卡死
func renderOCRTable(data map[string]any) error {
	if data == nil {
		fmt.Println("(empty result)")
		return nil
	}

	if name, ok := data["file_name"].(string); ok && name != "" {
		fmt.Printf("File:    %s\n", name)
	}
	if billCount, ok := data["bill_count"].(float64); ok {
		fmt.Printf("Bills:   %d\n", int(billCount))
	}
	if dur, ok := data["processing_duration_ms"].(float64); ok {
		fmt.Printf("Took:    %dms\n", int(dur))
	}

	result, ok := data["result"].(map[string]any)
	if !ok {
		return writeJSON(map[string]any{"data": data})
	}
	pages, ok := result["pages"].([]any)
	if !ok || len(pages) == 0 {
		return writeJSON(map[string]any{"data": data})
	}

	for _, p := range pages {
		page, ok := p.(map[string]any)
		if !ok {
			continue
		}
		pageNo := 0
		if n, ok := page["page_number"].(float64); ok {
			pageNo = int(n)
		}
		bills, ok := page["bills"].([]any)
		if !ok {
			continue
		}
		for billIdx, b := range bills {
			bill, ok := b.(map[string]any)
			if !ok {
				continue
			}
			typeDesc, _ := bill["type_description"].(string)
			if typeDesc == "" {
				typeDesc, _ = bill["type"].(string)
			}
			fmt.Printf("\nPage %d / Bill %d / %s\n", pageNo, billIdx+1, typeDesc)

			items, _ := bill["items"].([]any)
			rows := make([][]string, 0, len(items))
			for _, it := range items {
				item, ok := it.(map[string]any)
				if !ok {
					continue
				}
				value, _ := item["value"].(string)
				if value == "" {
					continue
				}
				label, _ := item["label"].(string)
				if label == "" {
					label, _ = item["key"].(string)
				}
				rows = append(rows, []string{label, value})
			}
			if len(rows) == 0 {
				fmt.Println("  (no recognized fields)")
				continue
			}
			table := tablewriter.NewTable(os.Stdout)
			table.Header("LABEL", "VALUE")
			_ = table.Bulk(rows)
			_ = table.Render()
		}
	}
	return nil
}
