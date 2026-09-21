/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、internal/api（Client/WithAppRole/RoleBeta/GetApp/ListEntities/ListRelations/Entity/Relation/UniqueConstraint/RelationProperties/RelationEnd）、cmd/apply（loadManifestsFromFile/Dir/ResourceManifest/getFieldMap/extractUniqueConstraints）、cmd/output（resolveOutputFormat/writeJSON）、encoding/json、errors、fmt、os、reflect、slices、sort、strings
 * [OUTPUT]: 对外提供 newDiffCmd 函数、errDiffFound 哨兵错误
 * [POS]: cmd 模块的顶层 diff 命令，对比远端 Meta Server 上 App 的 beta 环境 DSL（Entity + Relation + 唯一性约束）与本地 YAML 文件的差异——本地代码经 deploy 只推 beta，故基线固定是 beta（client 带 WithAppRole(RoleBeta)，无 --env 旋钮）；Entity 按 Key 匹配，Field 按 Key、唯一性约束按 name（字段顺序敏感）匹配；有差异时返回 errDiffFound（由 Execute 转译为退出码 1），实现 CI 漂移门禁；目录扫描与 apply 同款 --max-depth（默认 2，0=不限），确保 diff/apply 对「哪些文件构成 app」判定一致
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

// ---------------------------------- 哨兵错误 ----------------------------------

// errDiffFound 表示检测到差异（drift）。它沿 cobra RunE 链向上返回，
// 由 main.go 转译为退出码 1，使 CI 能据此门禁；不属于真正的执行失败，
// 故 reportExecuteError（单一错误出口）放过它不打印 error: 行。
var errDiffFound = errors.New("diff: differences found")

// ---------------------------------- 命令定义 ----------------------------------

func newDiffCmd() *cobra.Command {
	var path string
	var output string
	var maxDepth int

	cmd := &cobra.Command{
		Use:   "diff -f <path>",
		Short: "Compare local DSL files with the app's beta environment",
		Long: `Compare local YAML resource definitions with the app's beta environment on Meta Server.
Local code is deployed to beta (production is only reached via app promote), so beta is the
baseline. The app key is inferred from the Make.App manifest or entity's appKey field.`,
		Example: `  makecli diff -f ./dsl/
  makecli diff --file app.yaml --output json`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(path, output, maxDepth)
		},
	}

	cmd.Flags().StringVarP(&path, "file", "f", "", "path to YAML file or directory (required)")
	addOutputFlag(cmd, &output)
	cmd.Flags().IntVar(&maxDepth, "max-depth", 2, "directory recursion depth (1=top level, 2=+subdirs, 0=unlimited)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// ---------------------------------- Diff 数据类型 ----------------------------------

// DiffResult 整体对比结果
type DiffResult struct {
	AppKey    string         `json:"appKey"`
	Entities  []EntityDiff   `json:"entities"`
	Relations []RelationDiff `json:"relations"`
	Summary   DiffSummary    `json:"summary"`
}

// EntityDiff 单个 Entity 的对比结果（按 Key 标识）
type EntityDiff struct {
	Key         string           `json:"key"`
	Status      string           `json:"status"` // added | removed | changed | unchanged
	Fields      []FieldDiff      `json:"fields,omitempty"`
	Constraints []ConstraintDiff `json:"constraints,omitempty"`
}

// ConstraintDiff 单个唯一性约束的对比结果（按 name 标识）
type ConstraintDiff struct {
	Name   string `json:"name"`
	Status string `json:"status"` // added | removed | changed
	Detail string `json:"detail,omitempty"`
}

// FieldDiff 单个 Field 的对比结果（按 Key 标识）
type FieldDiff struct {
	Key    string `json:"key"`
	Status string `json:"status"` // added | removed | changed
	Detail string `json:"detail,omitempty"`
}

// RelationDiff 单个 Relation 的对比结果（按 Key 标识）
type RelationDiff struct {
	Key    string `json:"key"`
	Status string `json:"status"` // added | removed | changed | unchanged
	Detail string `json:"detail,omitempty"`
}

// DiffSummary 差异统计
type DiffSummary struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
}

// ---------------------------------- 状态常量 ----------------------------------

const (
	diffAdded     = "added"     // 仅本地有
	diffRemoved   = "removed"   // 仅远端有
	diffChanged   = "changed"   // 两端都有但不同
	diffUnchanged = "unchanged" // 完全一致
)

// ---------------------------------- 执行函数 ----------------------------------

func runDiff(path, output string, maxDepth int) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	if maxDepth < 0 {
		return fmt.Errorf("--max-depth 不能为负数（0=递归全部，1=当前目录，2=含子目录）")
	}

	// 构建客户端
	// 基线固定是 beta：本地代码经 deploy 只到 beta，production 由 promote 发布
	client, err := newClientFromProfile(api.WithAppRole(api.RoleBeta))
	if err != nil {
		return err
	}

	// 加载本地资源
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("路径不存在: %w", err)
	}
	var resources []ResourceManifest
	if info.IsDir() {
		resources, err = loadManifestsFromDir(path, maxDepth)
	} else {
		resources, err = loadManifestsFromFile(path)
	}
	if err != nil {
		return err
	}

	// 从 YAML 推断 app key
	appKey := resolveAppKey(resources)
	if appKey == "" {
		return fmt.Errorf("无法推断 App key：YAML 中未找到 Make.App 定义或 entity 的 appKey 字段")
	}

	// 提取本地 Entity / Relation 清单
	localEntities := filterEntities(resources)
	localRelations := filterRelations(resources)

	// 获取远端数据
	if _, err := client.GetApp(appKey); err != nil {
		return fmt.Errorf("获取远端 App '%s' 失败: %w", appKey, err)
	}
	remoteEntities, err := fetchAllEntities(client, appKey)
	if err != nil {
		return fmt.Errorf("获取远端 Entity 失败: %w", err)
	}
	remoteRelations, err := fetchAllRelations(client, appKey)
	if err != nil {
		return fmt.Errorf("获取远端 Relation 失败: %w", err)
	}

	// 计算差异
	entityResult := computeDiff(appKey, localEntities, remoteEntities)
	relationDiffs, relationSummary := computeRelationDiff(localRelations, remoteRelations)
	result := DiffResult{
		AppKey:    appKey,
		Entities:  entityResult.Entities,
		Relations: relationDiffs,
		Summary: DiffSummary{
			Added:     entityResult.Summary.Added + relationSummary.Added,
			Removed:   entityResult.Summary.Removed + relationSummary.Removed,
			Changed:   entityResult.Summary.Changed + relationSummary.Changed,
			Unchanged: entityResult.Summary.Unchanged + relationSummary.Unchanged,
		},
	}

	// 先算一次「是否有差异」，让两种输出模式共享同一退出决策
	hasDiff := result.Summary.Added > 0 || result.Summary.Removed > 0 || result.Summary.Changed > 0

	// 输出（JSON / 表格），writeJSON 的错误照常向上传播
	if output == outputJSON {
		if err := writeJSON(result); err != nil {
			return err
		}
	} else {
		renderDiffTable(&result)
	}

	// 有差异时返回哨兵错误 → Execute 转译为退出码 1（不依赖 os.Exit，可单测）
	if hasDiff {
		return errDiffFound
	}
	return nil
}

// ---------------------------------- 远端数据获取 ----------------------------------

// fetchAllEntities 分页获取指定 App 下的全部 Entity
func fetchAllEntities(client *api.Client, app string) ([]api.Entity, error) {
	var all []api.Entity
	page := 1
	for {
		batch, total, err := client.ListEntities(app, page, 100, "")
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(all) >= total {
			break
		}
		page++
	}
	return all, nil
}

// ---------------------------------- 本地数据过滤 ----------------------------------

// filterEntities 从混合资源清单中提取 Entity 类型的 Manifest
func filterEntities(resources []ResourceManifest) []ResourceManifest {
	var entities []ResourceManifest
	for _, r := range resources {
		if r.Type == "Make.Entity" {
			entities = append(entities, r)
		}
	}
	return entities
}

// filterRelations 从混合资源清单中提取 Relation 类型的 Manifest
func filterRelations(resources []ResourceManifest) []ResourceManifest {
	var relations []ResourceManifest
	for _, r := range resources {
		if r.Type == "Make.Relation" {
			relations = append(relations, r)
		}
	}
	return relations
}

// fetchAllRelations 分页获取指定 App 下的全部 Relation
func fetchAllRelations(client *api.Client, app string) ([]api.Relation, error) {
	var all []api.Relation
	page := 1
	for {
		batch, total, err := client.ListRelations(app, page, 100, "")
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(all) >= total {
			break
		}
		page++
	}
	return all, nil
}

// resolveAppKey 从资源清单推断 App key
// 优先级: Make.App 的 key > 第一个 Make.Entity 的 appKey 字段 > 第一个 Make.Relation 的 appKey 字段
func resolveAppKey(resources []ResourceManifest) string {
	for _, r := range resources {
		if r.Type == "Make.App" && r.Key != "" {
			return r.Key
		}
	}
	for _, r := range resources {
		if (r.Type == "Make.Entity" || r.Type == "Make.Relation") && r.AppKey != "" {
			return r.AppKey
		}
	}
	return ""
}

// ---------------------------------- 核心对比 ----------------------------------

// computeDiff 对比本地和远端的 Entity 集合，产出 DiffResult（按 Key 匹配）
func computeDiff(appKey string, local []ResourceManifest, remote []api.Entity) DiffResult {
	// 建索引（按 Key）
	remoteByKey := make(map[string]api.Entity, len(remote))
	for _, e := range remote {
		remoteByKey[e.Key] = e
	}
	localByKey := make(map[string]ResourceManifest, len(local))
	for _, m := range local {
		localByKey[m.Key] = m
	}

	var diffs []EntityDiff
	visited := make(map[string]bool)

	// 遍历本地: 找 added 和 changed
	for _, m := range local {
		visited[m.Key] = true
		re, exists := remoteByKey[m.Key]
		if !exists {
			diffs = append(diffs, EntityDiff{Key: m.Key, Status: diffAdded})
			continue
		}
		fieldDiffs := compareFields(m, &re)
		constraintDiffs := compareConstraints(m, &re)
		status := diffUnchanged
		if len(fieldDiffs) > 0 || len(constraintDiffs) > 0 {
			status = diffChanged
		}
		diffs = append(diffs, EntityDiff{Key: m.Key, Status: status, Fields: fieldDiffs, Constraints: constraintDiffs})
	}

	// 遍历远端: 找 removed
	for _, e := range remote {
		if visited[e.Key] {
			continue
		}
		diffs = append(diffs, EntityDiff{Key: e.Key, Status: diffRemoved})
	}

	// 排序: changed > added > removed > unchanged
	sort.Slice(diffs, func(i, j int) bool {
		return diffOrder(diffs[i].Status) < diffOrder(diffs[j].Status)
	})

	// 统计
	var summary DiffSummary
	for _, d := range diffs {
		switch d.Status {
		case diffAdded:
			summary.Added++
		case diffRemoved:
			summary.Removed++
		case diffChanged:
			summary.Changed++
		case diffUnchanged:
			summary.Unchanged++
		}
	}

	return DiffResult{AppKey: appKey, Entities: diffs, Summary: summary}
}

// computeRelationDiff 对比本地和远端的 Relation 集合（按 Key 匹配）
func computeRelationDiff(local []ResourceManifest, remote []api.Relation) ([]RelationDiff, DiffSummary) {
	remoteByKey := make(map[string]api.Relation, len(remote))
	for _, r := range remote {
		remoteByKey[r.Key] = r
	}

	var diffs []RelationDiff
	visited := make(map[string]bool)

	for _, m := range local {
		visited[m.Key] = true
		rr, exists := remoteByKey[m.Key]
		if !exists {
			diffs = append(diffs, RelationDiff{Key: m.Key, Status: diffAdded})
			continue
		}
		detail := compareRelationEndpoints(m, &rr)
		status := diffUnchanged
		if detail != "" {
			status = diffChanged
		}
		diffs = append(diffs, RelationDiff{Key: m.Key, Status: status, Detail: detail})
	}

	for _, r := range remote {
		if visited[r.Key] {
			continue
		}
		diffs = append(diffs, RelationDiff{Key: r.Key, Status: diffRemoved})
	}

	sort.Slice(diffs, func(i, j int) bool {
		return diffOrder(diffs[i].Status) < diffOrder(diffs[j].Status)
	})

	var summary DiffSummary
	for _, d := range diffs {
		switch d.Status {
		case diffAdded:
			summary.Added++
		case diffRemoved:
			summary.Removed++
		case diffChanged:
			summary.Changed++
		case diffUnchanged:
			summary.Unchanged++
		}
	}

	return diffs, summary
}

// compareRelationEndpoints 对比 Relation 的 from/to 端点（按 entityKey 比对），返回变化描述
func compareRelationEndpoints(local ResourceManifest, remote *api.Relation) string {
	localFrom := getFieldMap(local.Properties, "from")
	localTo := getFieldMap(local.Properties, "to")

	var changes []string

	localFromEntityKey, _ := localFrom["entityKey"].(string)
	localFromCard, _ := localFrom["cardinality"].(string)
	if localFromEntityKey != remote.Properties.From.EntityKey || localFromCard != remote.Properties.From.Cardinality {
		changes = append(changes, fmt.Sprintf("from: %s(%s) → %s(%s)",
			remote.Properties.From.EntityKey, remote.Properties.From.Cardinality,
			localFromEntityKey, localFromCard))
	}

	localToEntityKey, _ := localTo["entityKey"].(string)
	localToCard, _ := localTo["cardinality"].(string)
	if localToEntityKey != remote.Properties.To.EntityKey || localToCard != remote.Properties.To.Cardinality {
		changes = append(changes, fmt.Sprintf("to: %s(%s) → %s(%s)",
			remote.Properties.To.EntityKey, remote.Properties.To.Cardinality,
			localToEntityKey, localToCard))
	}

	return strings.Join(changes, "; ")
}

// diffOrder 差异状态排序权重
func diffOrder(status string) int {
	switch status {
	case diffChanged:
		return 0
	case diffAdded:
		return 1
	case diffRemoved:
		return 2
	case diffUnchanged:
		return 3
	default:
		return 4
	}
}

// compareFields 对比本地 Manifest 和远端 Entity 的字段列表（按 Key 匹配）
func compareFields(local ResourceManifest, remote *api.Entity) []FieldDiff {
	// 解析本地 fields
	localFields := extractLocalFields(local)

	// 构建远端索引（按 Key）
	remoteByKey := make(map[string]api.Field, len(remote.Properties.Fields))
	for _, f := range remote.Properties.Fields {
		remoteByKey[f.Key] = f
	}

	var diffs []FieldDiff
	visited := make(map[string]bool)

	// 本地 → 远端
	for _, lf := range localFields {
		visited[lf.Key] = true
		rf, exists := remoteByKey[lf.Key]
		if !exists {
			diffs = append(diffs, FieldDiff{
				Key:    lf.Key,
				Status: diffAdded,
				Detail: lf.Type,
			})
			continue
		}
		if detail := fieldChanges(lf, rf); detail != "" {
			diffs = append(diffs, FieldDiff{
				Key:    lf.Key,
				Status: diffChanged,
				Detail: detail,
			})
		}
	}

	// 远端 → 本地
	for _, rf := range remote.Properties.Fields {
		if visited[rf.Key] {
			continue
		}
		diffs = append(diffs, FieldDiff{
			Key:    rf.Key,
			Status: diffRemoved,
			Detail: rf.Type,
		})
	}

	return diffs
}

// compareConstraints 对比本地 Manifest 与远端 Entity 的唯一性约束（按 name 匹配，字段顺序敏感）
func compareConstraints(local ResourceManifest, remote *api.Entity) []ConstraintDiff {
	localCons, _ := extractUniqueConstraints(local.Properties) // 与 extractLocalFields 同样宽容：结构异常视为无约束，由 apply 兜底报错

	remoteByName := make(map[string]api.UniqueConstraint, len(remote.Properties.UniqueConstraints))
	for _, c := range remote.Properties.UniqueConstraints {
		remoteByName[c.Name] = c
	}

	var diffs []ConstraintDiff
	visited := make(map[string]bool)

	for _, lc := range localCons {
		visited[lc.Name] = true
		rc, exists := remoteByName[lc.Name]
		if !exists {
			diffs = append(diffs, ConstraintDiff{Name: lc.Name, Status: diffAdded, Detail: strings.Join(lc.Fields, ", ")})
			continue
		}
		if !slices.Equal(lc.Fields, rc.Fields) {
			diffs = append(diffs, ConstraintDiff{
				Name:   lc.Name,
				Status: diffChanged,
				Detail: fmt.Sprintf("fields: [%s] → [%s]", strings.Join(rc.Fields, ", "), strings.Join(lc.Fields, ", ")),
			})
		}
	}

	for _, rc := range remote.Properties.UniqueConstraints {
		if visited[rc.Name] {
			continue
		}
		diffs = append(diffs, ConstraintDiff{Name: rc.Name, Status: diffRemoved, Detail: strings.Join(rc.Fields, ", ")})
	}

	return diffs
}

// localField 从 YAML manifest 提取的字段定义
type localField struct {
	Key         string
	Name        string
	Type        string
	Meta        map[string]any
	Properties  map[string]any
	Validations map[string]any
}

// extractLocalFields 从 ResourceManifest 的 properties.fields 解析出字段列表
func extractLocalFields(m ResourceManifest) []localField {
	fieldsRaw, ok := m.Properties["fields"]
	if !ok {
		return nil
	}
	fieldsSlice, ok := fieldsRaw.([]any)
	if !ok {
		return nil
	}

	fields := make([]localField, 0, len(fieldsSlice))
	for _, f := range fieldsSlice {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		key, _ := fm["key"].(string)
		name, _ := fm["name"].(string)
		typ, _ := fm["type"].(string)
		fields = append(fields, localField{
			Key:         key,
			Name:        name,
			Type:        typ,
			Meta:        getFieldMap(fm, "meta"),
			Properties:  getFieldMap(fm, "properties"),
			Validations: getFieldMap(fm, "validations"),
		})
	}
	return fields
}

// fieldChanges 对比两端同名字段，返回变化描述（空串表示无变化）
func fieldChanges(local localField, remote api.Field) string {
	if local.Type != remote.Type {
		return fmt.Sprintf("type: %s → %s", remote.Type, local.Type)
	}
	// Properties 深度对比（JSON 归一化解决 int/float64 差异）
	if !jsonDeepEqual(local.Properties, remote.Properties) {
		return "properties changed"
	}
	if !jsonDeepEqual(local.Validations, remote.Validations) {
		return "validations changed"
	}
	return ""
}

// jsonDeepEqual 通过 JSON 序列化归一化后对比，解决 YAML int vs JSON float64 的类型差异
func jsonDeepEqual(a, b any) bool {
	na := normalize(a)
	nb := normalize(b)
	return reflect.DeepEqual(na, nb)
}

// normalize 通过 JSON 往返消除类型差异
func normalize(v any) any {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}

// ---------------------------------- 表格渲染 ----------------------------------

// renderDiffTable 以人类可读的格式输出差异（按 Key 标识资源）
func renderDiffTable(result *DiffResult) {
	fmt.Printf("App: %s\n\n", result.AppKey)

	hasDiff := false

	// Entity 差异
	if len(result.Entities) > 0 {
		fmt.Println("Entities:")
		for _, e := range result.Entities {
			switch e.Status {
			case diffChanged:
				hasDiff = true
				fmt.Printf("  ~ %s\n", e.Key)
				for _, f := range e.Fields {
					switch f.Status {
					case diffAdded:
						fmt.Printf("    + %s: %s (only in local)\n", f.Key, f.Detail)
					case diffRemoved:
						fmt.Printf("    - %s: %s (only on server)\n", f.Key, f.Detail)
					case diffChanged:
						fmt.Printf("    ~ %s: %s\n", f.Key, f.Detail)
					}
				}
				for _, c := range e.Constraints {
					switch c.Status {
					case diffAdded:
						fmt.Printf("    + unique %s: %s (only in local)\n", c.Name, c.Detail)
					case diffRemoved:
						fmt.Printf("    - unique %s: %s (only on server)\n", c.Name, c.Detail)
					case diffChanged:
						fmt.Printf("    ~ unique %s: %s\n", c.Name, c.Detail)
					}
				}
			case diffAdded:
				hasDiff = true
				fmt.Printf("  + %s (only in local)\n", e.Key)
			case diffRemoved:
				hasDiff = true
				fmt.Printf("  - %s (only on server)\n", e.Key)
			}
		}
	}

	// Relation 差异
	if len(result.Relations) > 0 {
		fmt.Println("\nRelations:")
		for _, r := range result.Relations {
			switch r.Status {
			case diffChanged:
				hasDiff = true
				fmt.Printf("  ~ %s\n", r.Key)
				if r.Detail != "" {
					fmt.Printf("    %s\n", r.Detail)
				}
			case diffAdded:
				hasDiff = true
				fmt.Printf("  + %s (only in local)\n", r.Key)
			case diffRemoved:
				hasDiff = true
				fmt.Printf("  - %s (only on server)\n", r.Key)
			}
		}
	}

	if !hasDiff {
		fmt.Println("  No changes detected.")
	}

	// 汇总
	s := result.Summary
	fmt.Printf("\nSummary: %d changed, %d added, %d removed, %d unchanged\n",
		s.Changed, s.Added, s.Removed, s.Unchanged)
}
