package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// The usage endpoint is rate limited; viewers share one recent answer.
	usageCacheLifetime = time.Minute
	usageTimeout       = 20 * time.Second
	maxTokens          = 50
	// probeModel is the smallest model; a probe costs one output token.
	probeModel = "claude-haiku-4-5-20251001"
)

// tokenStore keeps long-lived Claude tokens from `claude setup-token` in the
// persisted home. The active token is mirrored into its own file, which the
// claude launcher reads on every start, so a switch applies to the next CLI
// process without restarting Denova or interrupting running ones.
type tokenStore struct {
	dir    string
	client *http.Client
	mu     sync.Mutex
	usage  map[string]usageEntry
}

var errTokenRejected = errors.New("token rejected")

type storedToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

type tokenFile struct {
	Active string        `json:"active"`
	Tokens []storedToken `json:"tokens"`
}

type usageWindow struct {
	Kind        string  `json:"kind"`
	Label       string  `json:"label,omitempty"`
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at,omitempty"`
}

type tokenUsage struct {
	Windows   []usageWindow `json:"windows"`
	Source    string        `json:"source"`
	CheckedAt time.Time     `json:"checked_at"`
	Error     string        `json:"error,omitempty"`
	// ErrorKey is "rejected" for an invalid or expired token, else "failed".
	ErrorKey string `json:"error_key,omitempty"`
}

type usageEntry struct {
	usage tokenUsage
	at    time.Time
}

type tokenView struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Masked    string      `json:"masked"`
	Active    bool        `json:"active"`
	CreatedAt time.Time   `json:"created_at"`
	Usage     *tokenUsage `json:"usage,omitempty"`
}

func newTokenStore() *tokenStore {
	dir := filepath.Join(envOr("HOME", "/data"), ".config", "denova-claude")
	// http.DefaultTransport honors HTTPS_PROXY, which some regions require.
	return &tokenStore{dir: dir, client: &http.Client{Timeout: usageTimeout}, usage: map[string]usageEntry{}}
}

func (s *tokenStore) list(w http.ResponseWriter, r *http.Request) {
	file, err := s.read()
	if err != nil {
		slog.Error("Read Claude tokens failed", "error", err)
		writeError(w, http.StatusInternalServerError, "token_store_failed")
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	views := make([]tokenView, len(file.Tokens))
	var wg sync.WaitGroup
	for i, token := range file.Tokens {
		views[i] = tokenView{ID: token.ID, Name: token.Name, Masked: maskToken(token.Token), Active: token.ID == file.Active, CreatedAt: token.CreatedAt}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverAndLog("token usage")
			usage := s.usageFor(r.Context(), token, refresh)
			views[i].Usage = &usage
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"tokens": views, "active": file.Active, "env_token": os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != ""})
}

func (s *tokenStore) add(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	token := strings.TrimSpace(body.Token)
	name := strings.TrimSpace(body.Name)
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, " \t\r\n") || len(name) > 80 {
		writeError(w, http.StatusBadRequest, "invalid_token")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.read()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_store_failed")
		return
	}
	for _, existing := range file.Tokens {
		if existing.Token == token {
			writeError(w, http.StatusConflict, "token_exists")
			return
		}
	}
	if len(file.Tokens) >= maxTokens {
		writeError(w, http.StatusConflict, "token_limit")
		return
	}
	if name == "" {
		name = fmt.Sprintf("Token %d", len(file.Tokens)+1)
	}
	id := newID()
	file.Tokens = append(file.Tokens, storedToken{ID: id, Name: name, Token: token, CreatedAt: time.Now().UTC()})
	if file.Active == "" {
		file.Active = id
	}
	if err := s.write(file); err != nil {
		slog.Error("Save Claude token failed", "error", err)
		writeError(w, http.StatusInternalServerError, "token_store_failed")
		return
	}
	slog.Info("Claude token added", "id", id, "active", file.Active == id)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// activate selects the token for new CLI processes; an empty ID falls back to
// the .env token or the signed-in account.
func (s *tokenStore) activate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.read()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_store_failed")
		return
	}
	if body.ID != "" && indexOf(file.Tokens, body.ID) < 0 {
		writeError(w, http.StatusNotFound, "token_not_found")
		return
	}
	file.Active = body.ID
	if err := s.write(file); err != nil {
		slog.Error("Switch Claude token failed", "error", err)
		writeError(w, http.StatusInternalServerError, "token_store_failed")
		return
	}
	slog.Info("Claude token switched", "id", body.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *tokenStore) remove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.read()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_store_failed")
		return
	}
	index := indexOf(file.Tokens, id)
	if index < 0 {
		writeError(w, http.StatusNotFound, "token_not_found")
		return
	}
	file.Tokens = append(file.Tokens[:index], file.Tokens[index+1:]...)
	if file.Active == id {
		file.Active = ""
	}
	if err := s.write(file); err != nil {
		slog.Error("Remove Claude token failed", "error", err)
		writeError(w, http.StatusInternalServerError, "token_store_failed")
		return
	}
	delete(s.usage, id)
	slog.Info("Claude token removed", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *tokenStore) usageFor(ctx context.Context, token storedToken, refresh bool) tokenUsage {
	s.mu.Lock()
	cached, ok := s.usage[token.ID]
	s.mu.Unlock()
	// A manual refresh still reuses answers from the last few seconds.
	if ok && (time.Since(cached.at) < 10*time.Second || (!refresh && time.Since(cached.at) < usageCacheLifetime)) {
		return cached.usage
	}
	usage, err := s.fetchUsage(ctx, token.Token)
	if err != nil {
		// Tokens limited to inference cannot read the usage endpoint; the
		// rate-limit headers of a one-token request carry the same windows.
		usage, err = s.probeUsage(ctx, token.Token)
	}
	if err != nil {
		slog.Warn("Read Claude token usage failed", "id", token.ID, "error", err)
		key := "failed"
		if errors.Is(err, errTokenRejected) {
			key = "rejected"
		}
		usage = tokenUsage{Error: err.Error(), ErrorKey: key}
	}
	usage.CheckedAt = time.Now().UTC()
	s.mu.Lock()
	s.usage[token.ID] = usageEntry{usage: usage, at: time.Now()}
	s.mu.Unlock()
	return usage
}

func (s *tokenStore) fetchUsage(ctx context.Context, token string) (tokenUsage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return tokenUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	res, err := s.client.Do(req)
	if err != nil {
		return tokenUsage{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return tokenUsage{}, fmt.Errorf("usage endpoint returned %d", res.StatusCode)
	}
	type window struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	var body struct {
		FiveHour *window `json:"five_hour"`
		SevenDay *window `json:"seven_day"`
		Limits   []struct {
			Kind     string  `json:"kind"`
			Percent  float64 `json:"percent"`
			ResetsAt string  `json:"resets_at"`
			Scope    *struct {
				Model *struct {
					DisplayName string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return tokenUsage{}, fmt.Errorf("decode usage: %w", err)
	}
	usage := tokenUsage{Source: "usage"}
	add := func(kind string, w *window) {
		if w != nil && w.Utilization != nil {
			usage.Windows = append(usage.Windows, usageWindow{Kind: kind, Utilization: *w.Utilization, ResetsAt: w.ResetsAt})
		}
	}
	add("five_hour", body.FiveHour)
	add("seven_day", body.SevenDay)
	// Model-scoped weekly limits exist only in the generic limits list.
	for _, limit := range body.Limits {
		if limit.Kind == "weekly_scoped" && limit.Scope != nil && limit.Scope.Model != nil && limit.Scope.Model.DisplayName != "" {
			usage.Windows = append(usage.Windows, usageWindow{Kind: "seven_day_model", Label: limit.Scope.Model.DisplayName, Utilization: limit.Percent, ResetsAt: limit.ResetsAt})
		}
	}
	if len(usage.Windows) == 0 {
		return tokenUsage{}, errors.New("usage response has no limit windows")
	}
	return usage, nil
}

func (s *tokenStore) probeUsage(ctx context.Context, token string) (tokenUsage, error) {
	payload := fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"."}]}`, probeModel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(payload))
	if err != nil {
		return tokenUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return tokenUsage{}, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	usage := tokenUsage{Source: "probe"}
	for _, window := range []struct{ kind, prefix string }{{"five_hour", "5h"}, {"seven_day", "7d"}} {
		value := res.Header.Get("anthropic-ratelimit-unified-" + window.prefix + "-utilization")
		if value == "" {
			continue
		}
		utilization, err := strconv.ParseFloat(value, 64)
		if err != nil {
			continue
		}
		item := usageWindow{Kind: window.kind, Utilization: utilization * 100}
		if reset, err := strconv.ParseInt(res.Header.Get("anthropic-ratelimit-unified-"+window.prefix+"-reset"), 10, 64); err == nil {
			item.ResetsAt = time.Unix(reset, 0).UTC().Format(time.RFC3339)
		}
		usage.Windows = append(usage.Windows, item)
	}
	if len(usage.Windows) == 0 {
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			return tokenUsage{}, fmt.Errorf("%w: status %d", errTokenRejected, res.StatusCode)
		}
		return tokenUsage{}, fmt.Errorf("token check returned %d without usage limits", res.StatusCode)
	}
	return usage, nil
}

func (s *tokenStore) read() (tokenFile, error) {
	var file tokenFile
	data, err := os.ReadFile(filepath.Join(s.dir, "tokens.json"))
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return file, err
	}
	return file, json.Unmarshal(data, &file)
}

// write replaces both files atomically; the launcher only ever sees a whole token.
func (s *tokenStore) write(file tokenFile) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	active := ""
	if index := indexOf(file.Tokens, file.Active); index >= 0 {
		active = file.Tokens[index].Token
	}
	if err := writeFileAtomic(filepath.Join(s.dir, "tokens.json"), data); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.dir, "active-token"), []byte(active))
}

func writeFileAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func indexOf(tokens []storedToken, id string) int {
	for i, token := range tokens {
		if token.ID == id {
			return i
		}
	}
	return -1
}

func maskToken(token string) string {
	if len(token) <= 16 {
		return "…"
	}
	return token[:13] + "…" + token[len(token)-4:]
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
