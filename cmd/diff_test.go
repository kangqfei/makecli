/**
 * [INPUT]: 依赖 cmd 包内函数（包内白盒）、internal/config、internal/api、encoding/json、errors、io、net/http、net/http/httptest、os、path/filepath、strings、testing
 * [OUTPUT]: 覆盖 diff 子命令核心逻辑的单元测试（Entity + Relation + 唯一性约束 + 退出码契约：有差异返回 errDiffFound + 远端请求一律带 ?appRole=beta）
 * [POS]: cmd 模块顶层 diff 命令的配套测试，用 httptest 隔离网络、临时文件测试差异对比；自包含 stdout 劫持验证 JSON/表格两种输出模式的退出语义
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/api"
	"github.com/qfeius/makecli/internal/config"
)

// ---------------------------------- diff 测试 ----------------------------------

func TestRunDiff(t *testing.T) {
	t.Run("fails with missing credentials", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		dir := writeDiffYAML(t, entityYAML("Task", "myapp", "title", "Make.Field.Text"))

		err := runDiff(dir, outputTable, 2)
		if err == nil {
			t.Fatal("expected error for missing credentials")
		}
	})

	t.Run("fails with unknown profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		saveDiffToken(t)
		MetaServerURL = "http://unused"
		setProfile(t, "unknown")
		dir := writeDiffYAML(t, entityYAML("Task", "myapp", "title", "Make.Field.Text"))

		err := runDiff(dir, outputTable, 2)
		if err == nil {
			t.Fatal("expected error for unknown profile")
		}
	})

	t.Run("fails when remote app not found", func(t *testing.T) {
		srv := newDiffServer(t, nil, nil)
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDiffToken(t)
		MetaServerURL = srv.URL
		dir := writeDiffYAML(t, entityYAML("Task", "myapp", "title", "Make.Field.Text"))

		err := runDiff(dir, outputTable, 2)
		if err == nil {
			t.Fatal("expected error when remote app not found")
		}
	})

	t.Run("fails with invalid output format", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		dir := writeDiffYAML(t, entityYAML("Task", "myapp", "title", "Make.Field.Text"))

		err := runDiff(dir, "xml", 2)
		if err == nil {
			t.Fatal("expected error for invalid output format")
		}
	})
}

// ---------------------------------- runDiff 退出码契约测试 ----------------------------------
//
// 关键：有差异时 runDiff 必须返回 errDiffFound（而非 nil、也不调用 os.Exit），
// 否则 `diff --output json` 永远退出 0，CI 漂移门禁失效。os.Exit 会杀掉测试进程，
// 故退出决策必须以哨兵错误形式上抛、可单测。

func TestRunDiffExitContract(t *testing.T) {
	// setupDiffEnv 准备一个本地有差异的环境：远端为空（app 存在但无 entity），
	// 本地有一个 Task entity → 必然产生 added 差异。
	setupDiffEnv := func(t *testing.T, remoteEntities []api.Entity) string {
		t.Helper()
		srv := newDiffServer(t, remoteEntities, nil)
		t.Cleanup(srv.Close)
		t.Setenv("HOME", t.TempDir())
		saveDiffToken(t)
		MetaServerURL = srv.URL
		return writeDiffYAML(t, entityYAML("Task", "myapp", "title", "Make.Field.Text"))
	}

	t.Run("has-diff json path returns sentinel and emits valid JSON", func(t *testing.T) {
		// 远端有一个空 entity 列表（app 存在），本地多一个 Task → added=1
		dir := setupDiffEnv(t, []api.Entity{})

		out, err := captureRunDiffStdout(t, dir, outputJSON)
		if !errors.Is(err, errDiffFound) {
			t.Fatalf("expected errDiffFound, got %v", err)
		}
		var result DiffResult
		if jerr := json.Unmarshal([]byte(out), &result); jerr != nil {
			t.Fatalf("stdout is not valid JSON: %v\noutput: %q", jerr, out)
		}
		if result.Summary.Added != 1 {
			t.Errorf("expected JSON summary added=1, got %d", result.Summary.Added)
		}
		if result.AppKey != "myapp" {
			t.Errorf("expected appKey=myapp in JSON, got %q", result.AppKey)
		}
	})

	t.Run("has-diff table path returns sentinel", func(t *testing.T) {
		dir := setupDiffEnv(t, []api.Entity{})

		out, err := captureRunDiffStdout(t, dir, outputTable)
		if !errors.Is(err, errDiffFound) {
			t.Fatalf("expected errDiffFound, got %v", err)
		}
		if !strings.Contains(out, "Task") {
			t.Errorf("expected table output to mention Task entity, got %q", out)
		}
	})

	t.Run("no-diff json path returns nil and emits valid JSON", func(t *testing.T) {
		// 远端与本地完全一致 → 无差异
		remote := []api.Entity{
			makeRemoteEntity("Task", api.Field{Key: "title", Name: "title", Type: "Make.Field.Text"}),
		}
		dir := setupDiffEnv(t, remote)

		out, err := captureRunDiffStdout(t, dir, outputJSON)
		if err != nil {
			t.Fatalf("expected nil error for no-diff, got %v", err)
		}
		var result DiffResult
		if jerr := json.Unmarshal([]byte(out), &result); jerr != nil {
			t.Fatalf("stdout is not valid JSON: %v\noutput: %q", jerr, out)
		}
		if result.Summary.Added != 0 || result.Summary.Removed != 0 || result.Summary.Changed != 0 {
			t.Errorf("expected no diffs, got %+v", result.Summary)
		}
	})

	t.Run("no-diff table path returns nil", func(t *testing.T) {
		remote := []api.Entity{
			makeRemoteEntity("Task", api.Field{Key: "title", Name: "title", Type: "Make.Field.Text"}),
		}
		dir := setupDiffEnv(t, remote)

		if _, err := captureRunDiffStdout(t, dir, outputTable); err != nil {
			t.Fatalf("expected nil error for no-diff, got %v", err)
		}
	})
}

// captureRunDiffStdout 在劫持 os.Stdout 的前提下运行 runDiff，返回 stdout 内容与 runDiff 的错误。
// 自包含实现，不依赖外部 helper，避免与现有测试基建耦合。
func captureRunDiffStdout(t *testing.T, path, output string) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	runErr := runDiff(path, output, 2)

	_ = w.Close()
	os.Stdout = orig
	data, rerr := io.ReadAll(r)
	if rerr != nil {
		t.Fatalf("read captured stdout: %v", rerr)
	}
	return string(data), runErr
}

// ---------------------------------- computeDiff 测试 ----------------------------------

func TestComputeDiff(t *testing.T) {
	t.Run("no differences", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalEntity("Task", "title", "Make.Field.Text"),
		}
		remote := []api.Entity{
			makeRemoteEntity("Task", api.Field{Key: "title", Name: "title", Type: "Make.Field.Text"}),
		}

		result := computeDiff("myapp", local, remote)
		if result.Summary.Unchanged != 1 {
			t.Errorf("expected 1 unchanged, got %d", result.Summary.Unchanged)
		}
		if result.Summary.Added != 0 || result.Summary.Removed != 0 || result.Summary.Changed != 0 {
			t.Errorf("expected no diffs, got added=%d removed=%d changed=%d",
				result.Summary.Added, result.Summary.Removed, result.Summary.Changed)
		}
	})

	t.Run("entity only in local", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalEntity("NewEntity", "title", "Make.Field.Text"),
		}
		var remote []api.Entity

		result := computeDiff("myapp", local, remote)
		if result.Summary.Added != 1 {
			t.Errorf("expected 1 added, got %d", result.Summary.Added)
		}
		if result.Entities[0].Status != diffAdded {
			t.Errorf("expected status added, got %s", result.Entities[0].Status)
		}
	})

	t.Run("entity only on server", func(t *testing.T) {
		var local []ResourceManifest
		remote := []api.Entity{
			makeRemoteEntity("OldEntity", api.Field{Key: "title", Name: "title", Type: "Make.Field.Text"}),
		}

		result := computeDiff("myapp", local, remote)
		if result.Summary.Removed != 1 {
			t.Errorf("expected 1 removed, got %d", result.Summary.Removed)
		}
		if result.Entities[0].Status != diffRemoved {
			t.Errorf("expected status removed, got %s", result.Entities[0].Status)
		}
	})

	t.Run("field type changed", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalEntity("Task", "description", "Make.Field.TextArea"),
		}
		remote := []api.Entity{
			makeRemoteEntity("Task", api.Field{Key: "description", Name: "description", Type: "Make.Field.Text"}),
		}

		result := computeDiff("myapp", local, remote)
		if result.Summary.Changed != 1 {
			t.Errorf("expected 1 changed, got %d", result.Summary.Changed)
		}
		entity := result.Entities[0]
		if len(entity.Fields) != 1 {
			t.Fatalf("expected 1 field diff, got %d", len(entity.Fields))
		}
		if entity.Fields[0].Status != diffChanged {
			t.Errorf("expected field status changed, got %s", entity.Fields[0].Status)
		}
		if entity.Fields[0].Detail != "type: Make.Field.Text → Make.Field.TextArea" {
			t.Errorf("unexpected detail: %s", entity.Fields[0].Detail)
		}
	})

	t.Run("field added in local", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalEntityMultiFields("Task", []fieldDef{
				{"title", "Make.Field.Text"},
				{"newField", "Make.Field.Number"},
			}),
		}
		remote := []api.Entity{
			makeRemoteEntity("Task", api.Field{Key: "title", Name: "title", Type: "Make.Field.Text"}),
		}

		result := computeDiff("myapp", local, remote)
		if result.Summary.Changed != 1 {
			t.Errorf("expected 1 changed, got %d", result.Summary.Changed)
		}
		var addedField *FieldDiff
		for _, f := range result.Entities[0].Fields {
			if f.Key == "newField" {
				addedField = &f
				break
			}
		}
		if addedField == nil {
			t.Fatal("expected to find newField in diff")
		}
		if addedField.Status != diffAdded {
			t.Errorf("expected added, got %s", addedField.Status)
		}
	})

	t.Run("field removed from local", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalEntity("Task", "title", "Make.Field.Text"),
		}
		remote := []api.Entity{
			makeRemoteEntity("Task",
				api.Field{Key: "title", Name: "title", Type: "Make.Field.Text"},
				api.Field{Key: "oldField", Name: "oldField", Type: "Make.Field.Number"},
			),
		}

		result := computeDiff("myapp", local, remote)
		if result.Summary.Changed != 1 {
			t.Errorf("expected 1 changed, got %d", result.Summary.Changed)
		}
		var removedField *FieldDiff
		for _, f := range result.Entities[0].Fields {
			if f.Key == "oldField" {
				removedField = &f
				break
			}
		}
		if removedField == nil {
			t.Fatal("expected to find oldField in diff")
		}
		if removedField.Status != diffRemoved {
			t.Errorf("expected removed, got %s", removedField.Status)
		}
	})

	t.Run("mixed scenario", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalEntity("Unchanged", "title", "Make.Field.Text"),
			makeLocalEntity("Changed", "desc", "Make.Field.TextArea"),
			makeLocalEntity("OnlyLocal", "name", "Make.Field.Text"),
		}
		remote := []api.Entity{
			makeRemoteEntity("Unchanged", api.Field{Key: "title", Name: "title", Type: "Make.Field.Text"}),
			makeRemoteEntity("Changed", api.Field{Key: "desc", Name: "desc", Type: "Make.Field.Text"}),
			makeRemoteEntity("OnlyServer", api.Field{Name: "name", Type: "Make.Field.Text"}),
		}

		result := computeDiff("myapp", local, remote)
		if result.Summary.Unchanged != 1 {
			t.Errorf("expected 1 unchanged, got %d", result.Summary.Unchanged)
		}
		if result.Summary.Changed != 1 {
			t.Errorf("expected 1 changed, got %d", result.Summary.Changed)
		}
		if result.Summary.Added != 1 {
			t.Errorf("expected 1 added, got %d", result.Summary.Added)
		}
		if result.Summary.Removed != 1 {
			t.Errorf("expected 1 removed, got %d", result.Summary.Removed)
		}
	})

	t.Run("sort order: changed > added > removed > unchanged", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalEntity("Unchanged", "title", "Make.Field.Text"),
			makeLocalEntity("Added", "name", "Make.Field.Text"),
			makeLocalEntity("Changed", "desc", "Make.Field.TextArea"),
		}
		remote := []api.Entity{
			makeRemoteEntity("Unchanged", api.Field{Key: "title", Name: "title", Type: "Make.Field.Text"}),
			makeRemoteEntity("Removed", api.Field{Name: "name", Type: "Make.Field.Text"}),
			makeRemoteEntity("Changed", api.Field{Key: "desc", Name: "desc", Type: "Make.Field.Text"}),
		}

		result := computeDiff("myapp", local, remote)
		expected := []string{"Changed", "Added", "Removed", "Unchanged"}
		for i, e := range result.Entities {
			if e.Key != expected[i] {
				t.Errorf("position %d: expected %s, got %s", i, expected[i], e.Key)
			}
		}
	})
}

// ---------------------------------- uniqueConstraints diff 测试 ----------------------------------

func TestComputeDiffUniqueConstraints(t *testing.T) {
	con := func(name string, fields ...string) map[string]any {
		fs := make([]any, len(fields))
		for i, f := range fields {
			fs[i] = f
		}
		return map[string]any{"name": name, "fields": fs}
	}
	localWith := func(cons ...map[string]any) []ResourceManifest {
		m := makeLocalEntity("Member", "email", "Make.Field.Text")
		list := make([]any, len(cons))
		for i, c := range cons {
			list[i] = c
		}
		m.Properties["uniqueConstraints"] = list
		return []ResourceManifest{m}
	}
	remoteWith := func(cons ...api.UniqueConstraint) []api.Entity {
		e := makeRemoteEntity("Member", api.Field{Key: "email", Name: "email", Type: "Make.Field.Text"})
		e.Properties.UniqueConstraints = cons
		return []api.Entity{e}
	}

	t.Run("identical constraints → unchanged", func(t *testing.T) {
		result := computeDiff("myapp",
			localWith(con("uniq_email", "email")),
			remoteWith(api.UniqueConstraint{Name: "uniq_email", Fields: []string{"email"}}),
		)
		if result.Summary.Unchanged != 1 {
			t.Fatalf("expected unchanged, got %+v", result.Summary)
		}
		if len(result.Entities[0].Constraints) != 0 {
			t.Errorf("expected no constraint diffs, got %+v", result.Entities[0].Constraints)
		}
	})

	t.Run("constraint only in local → added + entity changed", func(t *testing.T) {
		result := computeDiff("myapp", localWith(con("uniq_email", "email")), remoteWith())
		if result.Summary.Changed != 1 {
			t.Fatalf("expected changed, got %+v", result.Summary)
		}
		cs := result.Entities[0].Constraints
		if len(cs) != 1 || cs[0].Name != "uniq_email" || cs[0].Status != diffAdded {
			t.Errorf("constraint diff = %+v, want uniq_email added", cs)
		}
	})

	t.Run("constraint only on server → removed", func(t *testing.T) {
		result := computeDiff("myapp", localWith(),
			remoteWith(api.UniqueConstraint{Name: "uniq_email", Fields: []string{"email"}}))
		cs := result.Entities[0].Constraints
		if len(cs) != 1 || cs[0].Status != diffRemoved {
			t.Errorf("constraint diff = %+v, want removed", cs)
		}
	})

	t.Run("field order is significant → changed", func(t *testing.T) {
		result := computeDiff("myapp",
			localWith(con("uniq_pm", "project_id", "member_id")),
			remoteWith(api.UniqueConstraint{Name: "uniq_pm", Fields: []string{"member_id", "project_id"}}),
		)
		cs := result.Entities[0].Constraints
		if len(cs) != 1 || cs[0].Status != diffChanged {
			t.Fatalf("constraint diff = %+v, want changed", cs)
		}
		if !strings.Contains(cs[0].Detail, "→") {
			t.Errorf("detail = %q, want before→after form", cs[0].Detail)
		}
	})
}

// ---------------------------------- fetchAllEntities 测试 ----------------------------------

func TestFetchAllEntities(t *testing.T) {
	t.Run("single page", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"msg":  "ok",
				"data": []map[string]any{
					{"key": "E1", "name": "E1", "type": "Make.Entity", "appKey": "a", "meta": map[string]any{}, "properties": map[string]any{"fields": []any{}}},
					{"key": "E2", "name": "E2", "type": "Make.Entity", "appKey": "a", "meta": map[string]any{}, "properties": map[string]any{"fields": []any{}}},
				},
				"pagination": map[string]any{"total": 2},
			})
		}))
		defer srv.Close()

		client := api.New(srv.URL, "tok")
		entities, err := fetchAllEntities(client, "a")
		if err != nil {
			t.Fatalf("fetchAllEntities: %v", err)
		}
		if len(entities) != 2 {
			t.Fatalf("expected 2, got %d", len(entities))
		}
	})

	t.Run("multi page", func(t *testing.T) {
		callCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")

			var data []map[string]any
			if callCount == 1 {
				data = []map[string]any{
					{"key": "E1", "name": "E1", "type": "Make.Entity", "appKey": "a", "meta": map[string]any{}, "properties": map[string]any{"fields": []any{}}},
				}
			} else {
				data = []map[string]any{
					{"key": "E2", "name": "E2", "type": "Make.Entity", "appKey": "a", "meta": map[string]any{}, "properties": map[string]any{"fields": []any{}}},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":       200,
				"msg":        "ok",
				"data":       data,
				"pagination": map[string]any{"total": 2},
			})
		}))
		defer srv.Close()

		client := api.New(srv.URL, "tok")
		entities, err := fetchAllEntities(client, "a")
		if err != nil {
			t.Fatalf("fetchAllEntities: %v", err)
		}
		if len(entities) != 2 {
			t.Fatalf("expected 2, got %d", len(entities))
		}
		if callCount != 2 {
			t.Fatalf("expected 2 API calls, got %d", callCount)
		}
	})
}

// ---------------------------------- jsonDeepEqual 测试 ----------------------------------

func TestJsonDeepEqual(t *testing.T) {
	t.Run("nil vs nil", func(t *testing.T) {
		if !jsonDeepEqual(nil, nil) {
			t.Error("nil should equal nil")
		}
	})

	t.Run("int vs float64 normalization", func(t *testing.T) {
		// YAML 解析 int, JSON 解析 float64
		if !jsonDeepEqual(42, 42.0) {
			t.Error("42 should equal 42.0 after normalization")
		}
	})

	t.Run("map comparison", func(t *testing.T) {
		a := map[string]any{"key": 1}
		b := map[string]any{"key": 1.0}
		if !jsonDeepEqual(a, b) {
			t.Error("maps should be equal after normalization")
		}
	})

	t.Run("different values", func(t *testing.T) {
		if jsonDeepEqual("a", "b") {
			t.Error("different strings should not be equal")
		}
	})
}

// ---------------------------------- 辅助函数 ----------------------------------

// fieldDef 测试用字段定义
type fieldDef struct {
	Name string
	Type string
}

// makeLocalEntity 构造包含单个字段的本地 Entity Manifest
func makeLocalEntity(name, fieldName, fieldType string) ResourceManifest {
	return makeLocalEntityMultiFields(name, []fieldDef{{fieldName, fieldType}})
}

// makeLocalEntityMultiFields 构造包含多个字段的本地 Entity Manifest（key 等同 name 简化测试）
func makeLocalEntityMultiFields(key string, fields []fieldDef) ResourceManifest {
	fs := make([]any, len(fields))
	for i, f := range fields {
		fs[i] = map[string]any{
			"key":        f.Name,
			"name":       f.Name,
			"type":       f.Type,
			"meta":       map[string]any{"version": "1.0.0"},
			"properties": nil,
		}
	}
	return ResourceManifest{
		Key:    key,
		Name:   key,
		Type:   "Make.Entity",
		AppKey: "myapp",
		Meta:   map[string]any{"version": "1.0.0"},
		Properties: map[string]any{
			"fields": fs,
		},
	}
}

// makeRemoteEntity 构造远端 Entity 对象（key 等同 name 简化测试）
func makeRemoteEntity(key string, fields ...api.Field) api.Entity {
	return api.Entity{
		Key:    key,
		Name:   key,
		Type:   "Make.Entity",
		AppKey: "myapp",
		Meta:   map[string]any{"version": "1.0.0"},
		Properties: api.EntityProperties{
			Fields: fields,
		},
	}
}

// entityYAML 生成单 Entity 的 YAML 字符串（key/name/appKey）
func entityYAML(key, appKey, fieldKey, fieldType string) string {
	return `key: ` + key + `
name: ` + key + `
type: Make.Entity
appKey: ` + appKey + `
meta:
  version: 1.0.0
properties:
  fields:
    - key: ` + fieldKey + `
      name: ` + fieldKey + `
      type: ` + fieldType + `
      meta:
        version: 1.0.0
      properties: null
`
}

// writeDiffYAML 写入 YAML 到临时目录，返回目录路径
func writeDiffYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "entity.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// saveDiffToken 写入测试用凭证
func saveDiffToken(t *testing.T) {
	t.Helper()
	fakeToken := "eyJ0eXAiOiJKV1QifQ.eyJzdWIiOiJ0ZXN0In0.c2lnbmF0dXJl"
	if err := config.Save(config.Credentials{
		"default": config.Profile{AccessToken: fakeToken},
	}); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------- computeRelationDiff 测试 ----------------------------------

func TestComputeRelationDiff(t *testing.T) {
	t.Run("no differences", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalRelation("rel1", "Project", "one", "Task", "many"),
		}
		remote := []api.Relation{
			makeRemoteRelation("rel1", "Project", "one", "Task", "many"),
		}

		diffs, summary := computeRelationDiff(local, remote)
		if summary.Unchanged != 1 {
			t.Errorf("expected 1 unchanged, got %d", summary.Unchanged)
		}
		if len(diffs) != 1 || diffs[0].Status != diffUnchanged {
			t.Errorf("expected unchanged status")
		}
	})

	t.Run("relation only in local", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalRelation("new-rel", "A", "one", "B", "many"),
		}
		var remote []api.Relation

		_, summary := computeRelationDiff(local, remote)
		if summary.Added != 1 {
			t.Errorf("expected 1 added, got %d", summary.Added)
		}
	})

	t.Run("relation only on server", func(t *testing.T) {
		var local []ResourceManifest
		remote := []api.Relation{
			makeRemoteRelation("old-rel", "A", "one", "B", "many"),
		}

		_, summary := computeRelationDiff(local, remote)
		if summary.Removed != 1 {
			t.Errorf("expected 1 removed, got %d", summary.Removed)
		}
	})

	t.Run("from endpoint changed", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalRelation("rel1", "ProjectV2", "one", "Task", "many"),
		}
		remote := []api.Relation{
			makeRemoteRelation("rel1", "Project", "one", "Task", "many"),
		}

		diffs, summary := computeRelationDiff(local, remote)
		if summary.Changed != 1 {
			t.Errorf("expected 1 changed, got %d", summary.Changed)
		}
		if !strings.Contains(diffs[0].Detail, "from:") {
			t.Errorf("expected detail to contain 'from:', got %q", diffs[0].Detail)
		}
	})

	t.Run("to cardinality changed", func(t *testing.T) {
		local := []ResourceManifest{
			makeLocalRelation("rel1", "Project", "one", "Task", "one"),
		}
		remote := []api.Relation{
			makeRemoteRelation("rel1", "Project", "one", "Task", "many"),
		}

		diffs, summary := computeRelationDiff(local, remote)
		if summary.Changed != 1 {
			t.Errorf("expected 1 changed, got %d", summary.Changed)
		}
		if !strings.Contains(diffs[0].Detail, "to:") {
			t.Errorf("expected detail to contain 'to:', got %q", diffs[0].Detail)
		}
	})
}

// ---------------------------------- fetchAllRelations 测试 ----------------------------------

func TestFetchAllRelations(t *testing.T) {
	t.Run("single page", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"msg":  "ok",
				"data": []map[string]any{
					{"key": "R1", "name": "R1", "type": "Make.Relation", "appKey": "a", "meta": map[string]any{}, "properties": map[string]any{"from": map[string]any{"entityKey": "A", "cardinality": "one"}, "to": map[string]any{"entityKey": "B", "cardinality": "many"}}},
				},
				"pagination": map[string]any{"total": 1},
			})
		}))
		defer srv.Close()

		client := api.New(srv.URL, "tok")
		relations, err := fetchAllRelations(client, "a")
		if err != nil {
			t.Fatalf("fetchAllRelations: %v", err)
		}
		if len(relations) != 1 {
			t.Fatalf("expected 1, got %d", len(relations))
		}
	})

	t.Run("multi page", func(t *testing.T) {
		callCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			var data []map[string]any
			if callCount == 1 {
				data = []map[string]any{
					{"key": "R1", "name": "R1", "type": "Make.Relation", "appKey": "a", "meta": map[string]any{}, "properties": map[string]any{"from": map[string]any{"entityKey": "A", "cardinality": "one"}, "to": map[string]any{"entityKey": "B", "cardinality": "many"}}},
				}
			} else {
				data = []map[string]any{
					{"key": "R2", "name": "R2", "type": "Make.Relation", "appKey": "a", "meta": map[string]any{}, "properties": map[string]any{"from": map[string]any{"entityKey": "C", "cardinality": "many"}, "to": map[string]any{"entityKey": "D", "cardinality": "many"}}},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":       200,
				"msg":        "ok",
				"data":       data,
				"pagination": map[string]any{"total": 2},
			})
		}))
		defer srv.Close()

		client := api.New(srv.URL, "tok")
		relations, err := fetchAllRelations(client, "a")
		if err != nil {
			t.Fatalf("fetchAllRelations: %v", err)
		}
		if len(relations) != 2 {
			t.Fatalf("expected 2, got %d", len(relations))
		}
		if callCount != 2 {
			t.Fatalf("expected 2 API calls, got %d", callCount)
		}
	})
}

// ---------------------------------- Relation 辅助函数 ----------------------------------

// makeLocalRelation 构造本地 Relation Manifest（key 等同展示名，entityKey 直接传入）
func makeLocalRelation(key, fromEntityKey, fromCard, toEntityKey, toCard string) ResourceManifest {
	return ResourceManifest{
		Key:    key,
		Name:   key,
		Type:   "Make.Relation",
		AppKey: "myapp",
		Meta:   map[string]any{"version": "1.0.0"},
		Properties: map[string]any{
			"from": map[string]any{"entityKey": fromEntityKey, "cardinality": fromCard},
			"to":   map[string]any{"entityKey": toEntityKey, "cardinality": toCard},
		},
	}
}

// makeRemoteRelation 构造远端 Relation 对象
func makeRemoteRelation(key, fromEntityKey, fromCard, toEntityKey, toCard string) api.Relation {
	return api.Relation{
		Key:    key,
		Name:   key,
		Type:   "Make.Relation",
		AppKey: "myapp",
		Meta:   map[string]any{"version": "1.0.0"},
		Properties: api.RelationProperties{
			From: api.RelationEnd{EntityKey: fromEntityKey, Cardinality: fromCard},
			To:   api.RelationEnd{EntityKey: toEntityKey, Cardinality: toCard},
		},
	}
}

// newDiffServer 创建 mock Meta Server，根据 X-Make-Target + URL path 路由请求
// remoteEntities 为 nil 时 GetApp 返回 404（app 不存在）
func newDiffServer(t *testing.T, remoteEntities []api.Entity, remoteRelations []api.Relation) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Make-Target")
		w.Header().Set("Content-Type", "application/json")
		// diff 的基线固定是 beta：每个远端请求都必须带 ?appRole=beta
		if got := r.URL.Query().Get("appRole"); got != api.RoleBeta {
			t.Errorf("%s %s: appRole query = %q, want %q", target, r.URL.Path, got, api.RoleBeta)
		}

		switch target {
		case "MakeService.GetResource":
			if remoteEntities == nil && remoteRelations == nil {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 404,
					"msg":  "not found",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"msg":  "ok",
				"data": map[string]any{
					"key":        "myapp",
					"name":       "myapp",
					"type":       "Make.App",
					"meta":       map[string]any{"version": "1.0.0"},
					"properties": map[string]any{},
				},
			})

		case "MakeService.ListResources":
			if strings.HasSuffix(r.URL.Path, "/meta/v1/relation") {
				relations := remoteRelations
				if relations == nil {
					relations = []api.Relation{}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":       200,
					"msg":        "ok",
					"data":       relations,
					"pagination": map[string]any{"total": len(relations)},
				})
			} else {
				entities := remoteEntities
				if entities == nil {
					entities = []api.Entity{}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":       200,
					"msg":        "ok",
					"data":       entities,
					"pagination": map[string]any{"total": len(entities)},
				})
			}

		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 400,
				"msg":  "unknown target",
			})
		}
	}))
}
