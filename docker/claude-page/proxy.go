package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var proxyNames = []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"}

// proxySettings edits the proxy in the env block of Claude's settings.json.
// The CLI applies that block itself, so one value serves both the CLI and this
// page; other settings keys are preserved untouched.
type proxySettings struct {
	path string
	mu   sync.Mutex
	// changed runs after a save so cached results from the old route go away.
	changed func()
}

func newProxySettings() *proxySettings {
	return &proxySettings{path: filepath.Join(envOr("HOME", "/data"), ".claude", "settings.json")}
}

// configured returns the proxy saved on this page, or "" when none is set.
func (p *proxySettings) configured() string {
	settings, err := p.read()
	if err != nil {
		return ""
	}
	env, _ := settings["env"].(map[string]any)
	for _, name := range proxyNames {
		if value, _ := env[name].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// transportProxy prefers the saved proxy, then the container's own variables.
func (p *proxySettings) transportProxy(r *http.Request) (*url.URL, error) {
	if value := p.configured(); value != "" {
		return url.Parse(value)
	}
	return http.ProxyFromEnvironment(r)
}

func (p *proxySettings) get(w http.ResponseWriter, _ *http.Request) {
	environment := ""
	for _, name := range proxyNames {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			environment = value
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"proxy": p.configured(), "environment": redactProxy(environment)})
}

func (p *proxySettings) set(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Proxy string `json:"proxy"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	value := strings.TrimSpace(body.Proxy)
	if value != "" {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") {
			writeError(w, http.StatusBadRequest, "invalid_proxy")
			return
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	settings, err := p.read()
	if err != nil {
		// Never replace a settings file that cannot be parsed.
		slog.Warn("Read Claude settings failed", "error", err)
		writeError(w, http.StatusConflict, "settings_unreadable")
		return
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	for _, name := range proxyNames {
		delete(env, name)
	}
	if value != "" {
		env["HTTPS_PROXY"], env["HTTP_PROXY"] = value, value
	}
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	if err := p.write(settings); err != nil {
		slog.Error("Save Claude proxy failed", "error", err)
		writeError(w, http.StatusInternalServerError, "settings_write_failed")
		return
	}
	slog.Info("Claude proxy updated", "configured", value != "")
	if p.changed != nil {
		p.changed()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *proxySettings) read() (map[string]any, error) {
	data, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	settings := map[string]any{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

// write keeps the first pre-edit copy as settings.json.denova-backup.
func (p *proxySettings) write(settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	backup := p.path + ".denova-backup"
	if original, err := os.ReadFile(p.path); err == nil {
		if _, statErr := os.Stat(backup); errors.Is(statErr, os.ErrNotExist) {
			if err := writeFileAtomic(backup, original); err != nil {
				return err
			}
		}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(p.path, append(data, '\n'))
}

func redactProxy(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	parsed.User = url.User("***")
	return parsed.String()
}
