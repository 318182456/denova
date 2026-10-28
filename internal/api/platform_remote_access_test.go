package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"denova/config"
	runtimeapp "denova/internal/app"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestRemotePluginPageNavigationPreservesAuthenticationAndManagementBoundaries(t *testing.T) {
	root := t.TempDir()
	hash, err := config.HashRemoteAccessPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	application, err := runtimeapp.New(context.Background(), &config.Config{
		OpenAIModel: "test-model", NovaDir: root, Workspace: root,
		OpenAIBaseURL: "http://127.0.0.1:1/v1", AllowLANAccess: true,
		RemoteAccessUsername: "reader", RemoteAccessPasswordHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	server := NewServer(application, "7789")
	login := remoteRequest(server.engine, http.MethodPost, "/api/auth/login", `{"username":"reader","password":"secret"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookies := (&http.Response{Header: http.Header{"Set-Cookie": []string{login.Header().Get("Set-Cookie")}}}).Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies: %d", len(cookies))
	}
	cookie := cookies[0].Name + "=" + cookies[0].Value
	const path = "/api/platform/manage/plugin-pages"
	for _, test := range []struct {
		name, method, path, cookie, origin string
		status                             int
	}{
		{"navigation", http.MethodGet, path, cookie, "http://localhost:7789", http.StatusOK},
		{"unauthenticated", http.MethodGet, path, "", "http://localhost:7789", http.StatusUnauthorized},
		{"isolated view", http.MethodGet, path, cookie, "http://localhost:49000", http.StatusForbidden},
		{"mutation", http.MethodPost, path, cookie, "http://localhost:7789", http.StatusForbidden},
		{"management", http.MethodGet, "/api/platform/manage/packages/plugin", cookie, "http://localhost:7789", http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := remoteRequest(server.engine, test.method, "http://localhost:7789"+test.path, "", test.cookie, ut.Header{Key: "Origin", Value: test.origin})
			if response.Code != test.status {
				t.Fatalf("response: %d %s, want %d", response.Code, response.Body.String(), test.status)
			}
			if test.status == http.StatusOK {
				var pages []any
				if err := json.Unmarshal(response.Body.Bytes(), &pages); err != nil || pages == nil {
					t.Fatalf("invalid navigation list: %s", response.Body.String())
				}
			}
		})
	}
}
