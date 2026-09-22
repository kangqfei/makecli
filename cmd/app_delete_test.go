/**
 * [INPUT]: 依赖 cmd 包内的 runAppDelete/runAppDeleteFromFile/resolveDeleteEnvs/confirmDeleteFunc（包内白盒），internal/api、encoding/json、errors、net/http、net/http/httptest、path/filepath、slices
 * [OUTPUT]: 覆盖 app delete 子命令核心逻辑的单元测试（含 -f 文件模式与删除确认门控）
 * [POS]: cmd 模块 app_delete.go 的配套测试，用 httptest 隔离网络（断言删除只走 DeleteResource 且 ?appRole= 与 --env 对应、零 GetResource）、t.Setenv 隔离凭证、打桩 confirmDeleteFunc 隔离终端交互
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

		if err := runAppDelete("myapp", api.EnvProduction, true); err != nil {
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

		if err := runAppDelete("myapp", api.EnvProduction, false); err != nil {
			t.Fatalf("runAppDelete: %v", err)
		}
	})

	t.Run("confirmation refusal stops before API", func(t *testing.T) {
		sentinel := errors.New("declined")
		stubConfirm(t, sentinel)
		// 没有 mock server：删除路径零读请求，确认失败必须在触网前短路
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = "http://unused"

		if err := runAppDelete("myapp", api.EnvProduction, false); !errors.Is(err, sentinel) {
			t.Fatalf("expected confirmation error, got %v", err)
		}
	})

	t.Run("real confirm gate refuses in non-interactive shell", func(t *testing.T) {
		// 不打桩，走真 confirmDeleteByTypingKey；go test 下 stdin 非 TTY，应直接拒绝
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		if err := runAppDelete("myapp", api.EnvProduction, false); err == nil {
			t.Fatal("expected refusal without --yes in non-interactive shell")
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		if err := runAppDelete("myapp", api.EnvProduction, true); err == nil {
			t.Fatal("expected error for missing credentials")
		}
	})

	t.Run("fails on API error response", func(t *testing.T) {
		srv := newMockMeta(t, 400, "app not found")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		if err := runAppDelete("myapp", api.EnvProduction, true); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("409 refusal surfaces the server message verbatim", func(t *testing.T) {
		// prod 仍有 beta 配对时服务端 409 拒删：给用户的就是服务端那句话，不套「唯一性」或环境前缀
		msg := "当前应用存在 Beta 环境，请先删除 Beta 环境，再删除正式环境。"
		srv := newMockMeta(t, 409, msg)
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		if err := runAppDelete("myapp", api.EnvProduction, true); err == nil || err.Error() != msg {
			t.Fatalf("err = %v, want exactly %q", err, msg)
		}
	})

	t.Run("fails with unknown profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = "http://unused"
		setProfile(t, "nonexistent")

		if err := runAppDelete("myapp", api.EnvProduction, true); err == nil {
			t.Fatal("expected error for unknown profile")
		}
	})
}

// newMockDeleteMeta 只认 DeleteResource：按序把 "<key>?appRole=<role>" 追加到 *deleted；
// 任何其他 target（尤其 GetResource）直接判失败——删除路径不得反查 app
func newMockDeleteMeta(t *testing.T, deleted *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if target := r.Header.Get("X-Make-Target"); target != "MakeService.DeleteResource" {
			t.Errorf("unexpected X-Make-Target %q: delete must not look up the app", target)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			Key string `json:"key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*deleted = append(*deleted, body.Key+"?appRole="+r.URL.Query().Get("appRole"))
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok", "data": true})
	}))
}

func TestResolveDeleteEnvs(t *testing.T) {
	tests := []struct {
		name, env string
		want      []string
		wantErr   bool
	}{
		{"production 只删 prod", api.EnvProduction, []string{api.EnvProduction}, false},
		{"beta 只删 beta", api.EnvBeta, []string{api.EnvBeta}, false},
		{"all 先 beta 后 production", envAll, []string{api.EnvBeta, api.EnvProduction}, false},
		{"空 env 拒绝", "", nil, true},
		{"未知 env 拒绝", "preview", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDeleteEnvs(tt.env)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("envs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunAppDeleteAll(t *testing.T) {
	// --env ALL（大小写不敏感）按 beta → prod 顺序删两次，key 始终是 prod key，确认只问一次
	var deleted []string
	srv := newMockDeleteMeta(t, &deleted)
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
	want := []string{"myapp?appRole=beta", "myapp?appRole=prod"}
	if !slices.Equal(deleted, want) || confirms != 1 {
		t.Errorf("deleted %v (want %v), confirms %d (want 1)", deleted, want, confirms)
	}
}

func TestRunAppDeleteBetaTarget(t *testing.T) {
	// --env beta：body 仍是用户给的 prod key，靠 ?appRole=beta 让服务端定位配对 app；确认表单也只见 prod key
	var deleted []string
	srv := newMockDeleteMeta(t, &deleted)
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	MetaServerURL = srv.URL

	var confirmed string
	orig := confirmDeleteFunc
	confirmDeleteFunc = func(k, _ string) error { confirmed = k; return nil }
	t.Cleanup(func() { confirmDeleteFunc = orig })

	if err := runAppDelete("myapp", api.EnvBeta, false); err != nil {
		t.Fatalf("runAppDelete: %v", err)
	}
	if confirmed != "myapp" || !slices.Equal(deleted, []string{"myapp?appRole=beta"}) {
		t.Errorf("confirmed %q (want myapp), deleted %v (want [myapp?appRole=beta])", confirmed, deleted)
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

		if err := runAppDeleteFromFile(f, api.EnvProduction, true); err != nil {
			t.Fatalf("runAppDeleteFromFile: %v", err)
		}
	})

	t.Run("fails on non-yaml file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "app.txt")
		writeTestFile(t, f, []byte("name: foo"))

		if err := runAppDeleteFromFile(f, api.EnvProduction, true); err == nil {
			t.Fatal("expected error for non-yaml file")
		}
	})

	t.Run("fails when no Make.App in file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "entity.yaml")
		writeTestFile(t, f, []byte("key: foo\nname: 实体\ntype: Make.Entity\nappKey: bar\n"))

		if err := runAppDeleteFromFile(f, api.EnvProduction, true); err == nil {
			t.Fatal("expected error for missing Make.App")
		}
	})
}
