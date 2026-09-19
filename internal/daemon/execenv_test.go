/**
 * [INPUT]: 依赖 execenv.go 的 PrepareWorkDir/BuildContextPrompt
 * [OUTPUT]: 对外提供执行环境回归——租户/Execution/generation 隔离、description 身份职责与 instructions 执行要求双文件渲染、保留服务端窗口角色和当前问题
 * [POS]: internal/daemon 的 execenv 测试面
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareWorkDirRendersInstructions(t *testing.T) {
	base := t.TempDir()
	claim := testClaim()
	claim.Agent = AgentBundle{
		Name: "助手", Description: "SRE 专家，精通 Kubernetes 与云原生。",
		Instructions: "永远说中文",
	}
	workDir, err := PrepareWorkDir(base, claim)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if filepath.Dir(workDir) != filepath.Join(base, "executions") {
		t.Fatalf("workDir = %q", workDir)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		content, err := os.ReadFile(filepath.Join(workDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		want := "# 助手\n\n## 身份与职责\n\nSRE 专家，精通 Kubernetes 与云原生。\n\n## 执行要求\n\n永远说中文\n"
		if string(content) != want {
			t.Fatalf("%s = %q", name, content)
		}
	}
}

func TestPrepareWorkDirRendersDescriptionWithoutInstructions(t *testing.T) {
	base := t.TempDir()
	claim := testClaim()
	claim.Agent = AgentBundle{Name: "SRE", Description: "负责 k8s 运维。"}
	workDir, err := PrepareWorkDir(base, claim)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if want := "# SRE\n\n## 身份与职责\n\n负责 k8s 运维。\n"; string(content) != want {
		t.Fatalf("AGENTS.md = %q, want %q", content, want)
	}
}

func TestPrepareWorkDirSeparatesTenantExecutionAndGeneration(t *testing.T) {
	base := t.TempDir()
	claim := testClaim()
	forbiddenDirectory := filepath.Join(t.TempDir(), "old")
	legacy, _ := json.Marshal(map[string]any{"resume": map[string]string{"workDir": forbiddenDirectory, "cliSessionID": "old-thread"}})
	if err := json.Unmarshal(legacy, &claim); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for index := range 4 {
		if index == 1 {
			claim.Execution.Lease.Generation++
		}
		if index == 2 {
			claim.Execution.Execution.ID = "another_execution"
			claim.Execution.Lease.ExecutionID = claim.Execution.Execution.ID
		}
		if index == 3 {
			claim.Context.Namespace.TenantID = "another_tenant"
		}
		directory, err := PrepareWorkDir(base, claim)
		if err != nil {
			t.Fatal(err)
		}
		if seen[directory] || directory == forbiddenDirectory {
			t.Fatal("execution workspace reused")
		}
		seen[directory] = true
	}
}

func TestContextPromptRejectsUnsupportedMedia(t *testing.T) {
	if _, err := BuildContextPrompt(ContextPack{Blocks: []ContextBlock{{Role: "user", Parts: []Block{{Kind: "image"}}}}}); err == nil {
		t.Fatal("image input silently became empty text")
	}
}

func TestContextPromptPreservesRolesAndCurrentQuestion(t *testing.T) {
	prompt, err := BuildContextPrompt(ContextPack{Blocks: []ContextBlock{{Role: "context", Content: "有来源的背景"}, {Role: "assistant", Content: "过去的答复"}, {Role: "user", Content: "当前问题"}}})
	if err != nil || prompt != "[context]\n有来源的背景\n\n[assistant]\n过去的答复\n\n[user]\n当前问题" {
		t.Fatalf("context projection: %q %v", prompt, err)
	}
}
