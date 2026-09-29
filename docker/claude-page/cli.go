package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	commandTimeout = 30 * time.Second
	// Downloading a native build can take minutes on slow links.
	updateTimeout = 10 * time.Minute
	// Authorization codes expire; an abandoned login must not linger forever.
	loginLifetime = 15 * time.Minute
	outputLimit   = 64 << 10
)

var (
	ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	urlPattern  = regexp.MustCompile(`https://\S+`)
)

// cli runs Claude Code account and installation commands for the page.
type cli struct {
	// busy serializes CLI mutations (login, logout, update) so they never race
	// on the shared credential and installation files.
	busy  sync.Mutex
	mu    sync.Mutex
	login *loginProcess
}

type authStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod,omitempty"`
	Email            string `json:"email,omitempty"`
	OrgName          string `json:"orgName,omitempty"`
	SubscriptionType string `json:"subscriptionType,omitempty"`
}

type statusResponse struct {
	Installed    bool        `json:"installed"`
	Path         string      `json:"path,omitempty"`
	Version      string      `json:"version,omitempty"`
	Auth         *authStatus `json:"auth,omitempty"`
	LoginPending bool        `json:"login_pending"`
	LoginURL     string      `json:"login_url,omitempty"`
}

func (s *cli) status(w http.ResponseWriter, r *http.Request) {
	result := statusResponse{}
	path, err := exec.LookPath("claude")
	if err == nil {
		result.Installed, result.Path = true, path
		result.Version = cliVersion(r.Context(), path)
		ctx, cancel := context.WithTimeout(r.Context(), commandTimeout)
		defer cancel()
		// auth status exits non-zero when signed out; the JSON body is authoritative.
		body, _ := command(ctx, path, "auth", "status").Output()
		var auth authStatus
		if json.Unmarshal(body, &auth) == nil {
			result.Auth = &auth
		} else {
			slog.Warn("Claude auth status unreadable")
		}
	}
	s.mu.Lock()
	if s.login != nil && !s.login.finished() {
		result.LoginPending, result.LoginURL = true, s.login.url
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

func (s *cli) loginStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Method string `json:"method"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var flag string
	switch body.Method {
	case "claudeai":
		flag = "--claudeai"
	case "console":
		flag = "--console"
	default:
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_installed")
		return
	}
	if !s.busy.TryLock() {
		writeError(w, http.StatusConflict, "busy")
		return
	}
	defer s.busy.Unlock()
	s.mu.Lock()
	if s.login != nil {
		s.login.cancel()
	}
	s.login = nil
	s.mu.Unlock()
	login, err := startLogin(r.Context(), path, flag)
	if err != nil {
		slog.Warn("Claude login did not provide an authorization URL", "error", err)
		writeError(w, http.StatusBadGateway, "login_failed")
		return
	}
	s.mu.Lock()
	s.login = login
	s.mu.Unlock()
	slog.Info("Claude login started", "method", body.Method)
	writeJSON(w, http.StatusOK, map[string]string{"url": login.url})
}

func (s *cli) loginCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body) != nil || strings.TrimSpace(body.Code) == "" || strings.ContainsAny(strings.TrimSpace(body.Code), "\r\n") {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.mu.Lock()
	login := s.login
	s.mu.Unlock()
	if login == nil || login.finished() {
		writeError(w, http.StatusConflict, "login_expired")
		return
	}
	if err := login.submit(r.Context(), strings.TrimSpace(body.Code)); err != nil {
		// CLI output may echo credentials, so only the outcome is logged.
		slog.Warn("Claude login code rejected", "error", err)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "output": login.outputText()})
		return
	}
	slog.Info("Claude login completed")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": login.outputText()})
}

func (s *cli) loginCancel(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	if s.login != nil {
		s.login.cancel()
		s.login = nil
		slog.Info("Claude login cancelled")
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *cli) logout(w http.ResponseWriter, r *http.Request) {
	s.runExclusive(w, r, commandTimeout, "logout", nil, "auth", "logout")
}

func (s *cli) update(w http.ResponseWriter, r *http.Request) {
	// The image disables background updates; an explicit update must still run.
	s.runExclusive(w, r, updateTimeout, "update", []string{"DISABLE_AUTOUPDATER"}, "update")
}

// runExclusive runs one CLI mutation and reports its combined output. It is
// detached from the request so a closed tab cannot leave a half-applied update.
func (s *cli) runExclusive(w http.ResponseWriter, r *http.Request, timeout time.Duration, operation string, unset []string, args ...string) {
	path, err := exec.LookPath("claude")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_installed")
		return
	}
	if !s.busy.TryLock() {
		writeError(w, http.StatusConflict, "busy")
		return
	}
	defer s.busy.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer cancel()
	cmd := command(ctx, path, args...)
	cmd.Env = withoutEnv(cmd.Env, unset...)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		slog.Warn("Claude CLI operation failed", "operation", operation, "error", runErr)
	} else {
		slog.Info("Claude CLI operation completed", "operation", operation)
	}
	// An update installs into the persisted home, which precedes the bundled CLI on PATH.
	version := ""
	if current, err := exec.LookPath("claude"); err == nil {
		version = cliVersion(ctx, current)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": runErr == nil, "output": cleanOutput(out), "version": version})
}

// loginProcess owns one `claude auth login` run. The CLI prints an authorization
// URL and then reads the code shown after authorization from stdin.
type loginProcess struct {
	url    string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	mu     sync.Mutex
	output strings.Builder
}

func startLogin(ctx context.Context, path, flag string) (*loginProcess, error) {
	processCtx, cancel := context.WithTimeout(context.Background(), loginLifetime)
	login := &loginProcess{cancel: cancel, done: make(chan struct{})}
	login.cmd = command(processCtx, path, "auth", "login", flag)
	reader, writer := io.Pipe()
	login.cmd.Stdout, login.cmd.Stderr = writer, writer
	stdin, err := login.cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	login.stdin = stdin
	if err := login.cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	found := make(chan string, 1)
	go func() {
		defer func() {
			if value := recover(); value != nil {
				slog.Error("Claude login output reader panicked", "panic", value)
				cancel()
			}
		}()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 4096), outputLimit)
		for scanner.Scan() {
			line := cleanOutput(scanner.Bytes())
			if match := urlPattern.FindString(line); match != "" && strings.Contains(match, "oauth") {
				select {
				case found <- match:
				default:
				}
				continue // The URL is returned separately and not kept in the output log.
			}
			login.appendOutput(line)
		}
		_, _ = io.Copy(io.Discard, reader)
	}()
	go func() {
		defer func() {
			if value := recover(); value != nil {
				slog.Error("Claude login waiter panicked", "panic", value)
			}
		}()
		err := login.cmd.Wait()
		_ = writer.Close()
		login.mu.Lock()
		login.err = err
		login.mu.Unlock()
		cancel()
		close(login.done)
	}()
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	select {
	case login.url = <-found:
		return login, nil
	case <-login.done:
		return nil, errors.New("login exited before printing an authorization URL")
	case <-timer.C:
	case <-ctx.Done():
	}
	cancel()
	return nil, errors.New("timed out waiting for the authorization URL")
}

func (l *loginProcess) submit(ctx context.Context, code string) error {
	if _, err := io.WriteString(l.stdin, code+"\n"); err != nil {
		return err
	}
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	select {
	case <-l.done:
	case <-timer.C:
		l.cancel()
		<-l.done
	case <-ctx.Done():
		l.cancel()
		<-l.done
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *loginProcess) finished() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

func (l *loginProcess) appendOutput(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.TrimSpace(line) != "" && l.output.Len() < outputLimit {
		l.output.WriteString(line + "\n")
	}
}

func (l *loginProcess) outputText() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.output.String()
}

func command(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = os.Environ()
	cmd.Dir = os.Getenv("HOME")
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func cliVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := command(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func cleanOutput(out []byte) string {
	text := ansiPattern.ReplaceAllString(string(out), "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if len(text) > outputLimit {
		text = text[len(text)-outputLimit:]
	}
	return text
}

func withoutEnv(env []string, names ...string) []string {
	out := env[:0:0]
	for _, item := range env {
		keep := true
		for _, name := range names {
			if strings.HasPrefix(item, name+"=") {
				keep = false
			}
		}
		if keep {
			out = append(out, item)
		}
	}
	return out
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("Claude page response write failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, key string) {
	writeJSON(w, status, map[string]string{"error": key, "message": fmt.Sprintf("claude page: %s", key)})
}
