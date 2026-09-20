/**
 * [INPUT]: 依赖 cmd 包内的 runAppDelete/runAppDeleteFromFile/resolveDeleteSteps/confirmDeleteFunc（包内白盒），internal/api、internal/config、encoding/json、errors、net/http、net/http/httptest、path/filepath、slices
 * [OUTPUT]: 覆盖 app delete 子命令核心逻辑的单元测试（含 -f 文件模式与删除确认门控）
 * [POS]: cmd 模块 app_delete.go 的配套测试，用 httptest 隔离网络、t.Setenv 隔离凭证、打桩 confirmDeleteFunc 隔离终端交互
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/qfeius/makecli/internal/api"
)

// stubConfirm 临时替换 confirmDeleteFunc，t.Cleanup 自动还原，隔离真实终端交互
func stubConfirm(t *testing.T, err error) {
	t.Helper()
	orig := confirmDeleteFunc
	confirmDeleteFunc = func(string, string) error { return err }
	t.Cleanup(func() { confirmDeleteFunc = orig })
}

func TestRunAppDelete(t *testing.T) {
	t.Run("deletes app via API with --yes", func(t *testing.T) {
		srv := newMockMeta(t, 200, "delete app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		if err := runAppDelete("myapp", appRoleProduction, true); err != nil {
			t.Fatalf("runAppDelete: %v", err)
		}
	})

	t.Run("deletes app after confirmation succeeds", func(t *testing.T) {
		stubConfirm(t, nil)
		srv := newMockMeta(t, 200, "delete app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		if err := runAppDelete("myapp", appRoleProduction, false); err != nil {
			t.Fatalf("runAppDelete: %v", err)
		}
	})

	t.Run("confirmation refusal stops before API", func(t *testing.T) {
		sentinel := errors.New("declined")
		stubConfirm(t, sentinel)
		// 没有 mock server：production 路径不需反查，确认失败必须在触网前短路
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = "http://unused"

		if err := runAppDelete("myapp", appRoleProduction, false); !errors.Is(err, sentinel) {
			t.Fatalf("expected confirmation error, got %v", err)
		}
	})

	t.Run("real confirm gate refuses in non-interactive shell", func(t *testing.T) {
		// 不打桩，走真 confirmDeleteByTypingKey；go test 下 stdin 非 TTY，应直接拒绝
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		if err := runAppDelete("myapp", appRoleProduction, false); err == nil {
			t.Fatal("expected refusal without --yes in non-interactive shell")
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		if err := runAppDelete("myapp", appRoleProduction, true); err == nil {
			t.Fatal("expected error for missing credentials")
		}
	})

	t.Run("fails on API error response", func(t *testing.T) {
		srv := newMockMeta(t, 400, "app not found")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		if err := runAppDelete("myapp", appRoleProduction, true); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("fails with unknown profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = "http://unused"
		setProfile(t, "nonexistent")

		if err := runAppDelete("myapp", appRoleProduction, true); err == nil {
			t.Fatal("expected error for unknown profile")
		}
	})
}

// newMockPairMeta 按 X-Make-Target 分流：GetResource 回给定 appRole/pairAppKey 的 app，DeleteResource 按序追加 key 到 *deleted
func newMockPairMeta(t *testing.T, role, pair string, deleted *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-Make-Target") {
		case "MakeService.GetResource":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok", "data": map[string]any{
				"key": "myapp", "name": "myapp", "type": "Make.App",
				"meta": map[string]any{"appRole": role, "pairAppKey": pair},
			}})
		case "MakeService.DeleteResource":
			var body struct {
				Key string `json:"key"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			*deleted = append(*deleted, body.Key)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok", "data": map[string]any{}})
		}
	}))
}

func TestResolveDeleteSteps(t *testing.T) {
	tests := []struct {
		name, env, role, pair string
		want                  []deleteStep
		wantErr               bool
	}{
		{"production 删本体，不反查", appRoleProduction, "", "", []deleteStep{{"myapp", appRoleProduction}}, false},
		{"beta 取服务端 pairAppKey", appRoleBeta, "prod", "myapp_beta_", []deleteStep{{"myapp_beta_", appRoleBeta}}, false},
		{"beta 但传入的是 beta app 拒绝，不反删 prod", appRoleBeta, "beta", "myapp", nil, true},
		{"beta 无配对拒绝", appRoleBeta, "prod", "", nil, true},
		{"all 先 beta 后 production", envAll, "prod", "myapp_beta_", []deleteStep{{"myapp_beta_", appRoleBeta}, {"myapp", appRoleProduction}}, false},
		{"all 无配对只剩 production", envAll, "prod", "", []deleteStep{{"myapp", appRoleProduction}}, false},
		{"all 传入 beta app 拒绝", envAll, "beta", "myapp", nil, true},
		{"空 env 拒绝", "", "", "", nil, true},
		{"未知 env 拒绝", "preview", "", "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var unused []string
			srv := newMockPairMeta(t, tt.role, tt.pair, &unused)
			defer srv.Close()
			got, err := resolveDeleteSteps(api.New(srv.URL, "t"), "myapp", tt.env)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("steps = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunAppDeleteAll(t *testing.T) {
	// --env ALL（大小写不敏感）按 beta → production 顺序删两次，确认只问一次
	var deleted []string
	srv := newMockPairMeta(t, "prod", "myapp_beta_", &deleted)
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	MetaServerURL = srv.URL

	confirms := 0
	orig := confirmDeleteFunc
	confirmDeleteFunc = func(_, _ string) error { confirms++; return nil }
	t.Cleanup(func() { confirmDeleteFunc = orig })

	if err := runAppDelete("myapp", "ALL", false); err != nil {
		t.Fatalf("runAppDelete: %v", err)
	}
	if !slices.Equal(deleted, []string{"myapp_beta_", "myapp"}) || confirms != 1 {
		t.Errorf("deleted %v (want [myapp_beta_ myapp]), confirms %d (want 1)", deleted, confirms)
	}
}

func TestRunAppDeleteBetaTarget(t *testing.T) {
	// --env beta 时 API 删除的是服务端给的 pairAppKey，而确认表单只见用户给的 app key（配对 key 不外露）
	var deleted []string
	srv := newMockPairMeta(t, "prod", "myapp_beta_", &deleted)
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	MetaServerURL = srv.URL

	var confirmed string
	orig := confirmDeleteFunc
	confirmDeleteFunc = func(k, _ string) error { confirmed = k; return nil }
	t.Cleanup(func() { confirmDeleteFunc = orig })

	if err := runAppDelete("myapp", appRoleBeta, false); err != nil {
		t.Fatalf("runAppDelete: %v", err)
	}
	if confirmed != "myapp" || !slices.Equal(deleted, []string{"myapp_beta_"}) {
		t.Errorf("confirmed %q (want myapp), deleted %v (want [myapp_beta_])", confirmed, deleted)
	}
}

func TestRunAppDeleteFromFile(t *testing.T) {
	t.Run("deletes app from YAML file", func(t *testing.T) {
		srv := newMockMeta(t, 200, "delete app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		f := filepath.Join(t.TempDir(), "app.yaml")
		writeTestFile(t, f, []byte("key: fileapp\nname: 文件应用\ntype: Make.App\n"))

		if err := runAppDeleteFromFile(f, appRoleProduction, true); err != nil {
			t.Fatalf("runAppDeleteFromFile: %v", err)
		}
	})

	t.Run("fails on non-yaml file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "app.txt")
		writeTestFile(t, f, []byte("name: foo"))

		if err := runAppDeleteFromFile(f, appRoleProduction, true); err == nil {
			t.Fatal("expected error for non-yaml file")
		}
	})

	t.Run("fails when no Make.App in file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "entity.yaml")
		writeTestFile(t, f, []byte("key: foo\nname: 实体\ntype: Make.Entity\nappKey: bar\n"))

		if err := runAppDeleteFromFile(f, appRoleProduction, true); err == nil {
			t.Fatal("expected error for missing Make.App")
		}
	})
}
