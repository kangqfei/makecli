/**
 * [INPUT]: 依赖 cmd 包内的 decodeJSONFlag / flagStdin（包内白盒），internal/api、os、path/filepath、strings、testing
 * [OUTPUT]: 覆盖 JSON 型 flag 三形态读取（inline/@file/stdin）与严格解码（未知字段拒绝/非法 JSON/文件不存在）的单元测试
 * [POS]: cmd 模块 jsonflag.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/api"
)

func TestDecodeJSONFlag(t *testing.T) {
	t.Run("inline JSON", func(t *testing.T) {
		var sort []api.SortField
		if err := decodeJSONFlag("sort-json", `[{"fieldKey":"createdAt","order":"desc"}]`, &sort); err != nil {
			t.Fatalf("decodeJSONFlag: %v", err)
		}
		if len(sort) != 1 || sort[0].FieldKey != "createdAt" || sort[0].Order != "desc" {
			t.Fatalf("unexpected sort: %+v", sort)
		}
	})

	t.Run("@file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "group.json")
		if err := os.WriteFile(path, []byte(`[{"fieldKey":"status"}]`), 0o600); err != nil {
			t.Fatal(err)
		}
		var group []api.GroupField
		if err := decodeJSONFlag("group-json", "@"+path, &group); err != nil {
			t.Fatalf("decodeJSONFlag: %v", err)
		}
		if len(group) != 1 || group[0].FieldKey != "status" {
			t.Fatalf("unexpected group: %+v", group)
		}
	})

	t.Run("- reads stdin", func(t *testing.T) {
		orig := flagStdin
		flagStdin = strings.NewReader(`[{"aggregate":"count","alias":"total"}]`)
		defer func() { flagStdin = orig }()

		var aggs []api.AggregateField
		if err := decodeJSONFlag("aggregates-json", "-", &aggs); err != nil {
			t.Fatalf("decodeJSONFlag: %v", err)
		}
		if len(aggs) != 1 || aggs[0].Alias != "total" {
			t.Fatalf("unexpected aggregates: %+v", aggs)
		}
	})

	t.Run("rejects unknown field", func(t *testing.T) {
		var group []api.GroupField
		err := decodeJSONFlag("group-json", `[{"fieldKey":"orderDate","granularit":"month"}]`, &group)
		if err == nil || !strings.Contains(err.Error(), "--group-json") || !strings.Contains(err.Error(), "granularit") {
			t.Fatalf("expected unknown-field error naming the flag and key, got %v", err)
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		var sort []api.SortField
		if err := decodeJSONFlag("sort-json", "createdAt:desc", &sort); err == nil {
			t.Fatal("expected error for non-JSON value")
		}
	})

	t.Run("fails on missing file", func(t *testing.T) {
		var sort []api.SortField
		err := decodeJSONFlag("sort-json", "@"+filepath.Join(t.TempDir(), "missing.json"), &sort)
		if err == nil || !strings.Contains(err.Error(), "--sort-json") {
			t.Fatalf("expected error naming the flag, got %v", err)
		}
	})
}
