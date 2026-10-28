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

func TestRemotePlatformMetadataPreservesAuthenticationAndHostEffectBoundaries(t *testing.T) {
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
	const base = "http://localhost:7789/api/platform/manage"
	for _, path := range []string{
		"/plugin-pages", "/catalog", "/instances", "/game-preferences",
		"/packages/plugin", "/packages/game", "/runtimes", "/development",
		"/projects/" + application.ProjectID() + "/extensions",
		"/projects/" + application.ProjectID() + "/plugins?context=writing&locale=en-US",
		"/projects/" + application.ProjectID() + "/plugins?context=game&locale=zh-CN",
	} {
		t.Run(path, func(t *testing.T) {
			for _, access := range []struct {
				name, cookie, referer string
				status                int
			}{
				{"authenticated", cookie, "http://localhost:7789/", http.StatusOK},
				{"unauthenticated", "", "http://localhost:7789/", http.StatusUnauthorized},
				{"isolated view", cookie, "http://localhost:49000/", http.StatusForbidden},
			} {
				t.Run(access.name, func(t *testing.T) {
					response := remoteRequest(server.engine, http.MethodGet, base+path, "", access.cookie, ut.Header{Key: "Referer", Value: access.referer})
					if response.Code != access.status {
						t.Fatalf("response: %d %s, want %d", response.Code, response.Body.String(), access.status)
					}
					if access.status == http.StatusOK && !json.Valid(response.Body.Bytes()) {
						t.Fatalf("invalid metadata response: %s", response.Body.String())
					}
				})
			}
		})
	}
	for _, operation := range []struct{ method, path, body string }{
		{http.MethodGet, "/development/test-source/check", ""},
		{http.MethodPost, "/packages/install", `{}`},
		{http.MethodPost, "/runtimes/plugin", `{}`},
		{http.MethodPost, "/instances", `{}`},
		{http.MethodDelete, "/instances/test-instance", ""},
		{http.MethodPut, "/packages/plugin/test/settings", `{}`},
	} {
		t.Run(operation.method+operation.path, func(t *testing.T) {
			response := remoteRequest(server.engine, operation.method, base+operation.path, operation.body, cookie)
			if response.Code != http.StatusForbidden {
				t.Fatalf("host effect: %d %s", response.Code, response.Body.String())
			}
		})
	}
	for _, test := range []struct {
		name, cookie, origin, body string
		status                     int
	}{
		{"save preference", cookie, "http://localhost:7789", `{"defaultGameId":"builtin.story"}`, http.StatusOK},
		{"invalid preference", cookie, "http://localhost:7789", `{"defaultGameId":"missing.game"}`, http.StatusConflict},
		{"unauthenticated preference", "", "http://localhost:7789", `{"defaultGameId":"builtin.story"}`, http.StatusUnauthorized},
		{"cross-site preference", cookie, "http://attacker.test", `{"defaultGameId":"builtin.story"}`, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := remoteRequest(server.engine, http.MethodPatch, base+"/game-preferences", test.body, test.cookie, ut.Header{Key: "Origin", Value: test.origin})
			if response.Code != test.status {
				t.Fatalf("preference: %d %s, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}
}
