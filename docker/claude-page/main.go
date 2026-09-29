// Command denova-claude-page fronts Denova in the container image. It serves a
// Claude Code sign-in and update page under /claude/ and reverse-proxies every
// other request to Denova, so the page shares Denova's port and sign-in without
// patching upstream sources.
//
// It also supervises Denova: the container stops when either side exits, which
// lets the restart policy recover both.
//
// Denova trusts X-Forwarded-For only from loopback peers. The proxy is such a
// peer, so it always replaces forwarded headers with the real peer address;
// otherwise any client could claim to be local and skip Denova's sign-in.
package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

//go:embed index.html
var indexHTML []byte

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if len(os.Args) < 2 {
		slog.Error("Denova command is required")
		os.Exit(2)
	}
	backend := &url.URL{Scheme: "http", Host: "127.0.0.1:" + envOr("DENOVA_BACKEND_PORT", "18080")}
	addr := ":" + envOr("DENOVA_PROXY_PORT", "8080")

	child := exec.Command(os.Args[1], os.Args[2:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		slog.Error("Start Denova failed", "error", err)
		os.Exit(1)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		defer recoverAndLog("signal forwarder")
		for sig := range signals {
			_ = child.Process.Signal(sig)
		}
	}()

	srv := &http.Server{Addr: addr, Handler: newHandler(backend), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		defer recoverAndLog("proxy server")
		slog.Info("Claude page and Denova proxy listening", "addr", addr, "backend", backend.Host)
		err := srv.ListenAndServe()
		slog.Error("Proxy server stopped; stopping Denova", "error", err)
		_ = child.Process.Signal(syscall.SIGTERM)
	}()

	err := child.Wait()
	var exit *exec.ExitError
	switch {
	case err == nil:
		os.Exit(0)
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		os.Exit(exit.ExitCode())
	default:
		slog.Error("Denova stopped", "error", err)
		os.Exit(1)
	}
}

func newHandler(backend *url.URL) http.Handler {
	page := &cli{}
	auth := &sessionCheck{backend: backend, client: &http.Client{Timeout: 10 * time.Second}}
	proxy := &httputil.ReverseProxy{
		// Rewrite drops inbound Forwarded and X-Forwarded-* headers, and
		// SetXForwarded records the direct peer as the client.
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(backend)
			r.SetXForwarded()
			r.Out.Host = r.In.Host
		},
		// Agent runs stream server-sent events; forward every write immediately.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("Denova proxy request failed", "path", r.URL.Path, "error", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/", proxy)
	mux.Handle("GET /claude", http.RedirectHandler("/claude/", http.StatusMovedPermanently))
	mux.HandleFunc("GET /claude/{$}", func(w http.ResponseWriter, r *http.Request) {
		if !auth.authenticated(r) {
			// Denova's own page signs in; the session cookie then covers this page.
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(indexHTML)
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /claude/api/status", page.status)
	api.HandleFunc("POST /claude/api/login/start", page.loginStart)
	api.HandleFunc("POST /claude/api/login/code", page.loginCode)
	api.HandleFunc("POST /claude/api/login/cancel", page.loginCancel)
	api.HandleFunc("POST /claude/api/logout", page.logout)
	api.HandleFunc("POST /claude/api/update", page.update)
	mux.Handle("/claude/api/", auth.guard(api))
	return mux
}

// sessionCheck asks Denova whether a request carries a valid sign-in, so the
// page never keeps credentials or sessions of its own.
type sessionCheck struct {
	backend *url.URL
	client  *http.Client
}

func (s *sessionCheck) authenticated(r *http.Request) bool {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.backend.JoinPath("/api/auth/status").String(), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Cookie", r.Header.Get("Cookie"))
	req.Header.Set("X-Forwarded-For", peerIP(r))
	res, err := s.client.Do(req)
	if err != nil {
		slog.Warn("Denova session check failed", "error", err)
		return false
	}
	defer res.Body.Close()
	var status struct {
		Authenticated bool `json:"authenticated"`
	}
	return res.StatusCode == http.StatusOK && json.NewDecoder(res.Body).Decode(&status) == nil && status.Authenticated
}

// guard requires a Denova session and, for mutations, a custom header that
// cross-site forms cannot send.
func (s *sessionCheck) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				slog.Error("Claude page request panicked", "path", r.URL.Path, "panic", value)
				writeError(w, http.StatusInternalServerError, "internal")
			}
		}()
		if !s.authenticated(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Denova-Claude") != "1" {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func recoverAndLog(name string) {
	if value := recover(); value != nil {
		slog.Error("Goroutine panicked", "name", name, "panic", value)
	}
}
