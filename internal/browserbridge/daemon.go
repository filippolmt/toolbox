package browserbridge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// requestTimeout caps how long the daemon waits for the URL handler to
// return. /usr/bin/open and xdg-open both fork+exit quickly, so anything
// past a few seconds means the host environment is wedged; we surface that
// to the wrapper instead of hanging the in-container CLI.
const requestTimeout = 5 * time.Second

// rateLimit is the maximum number of /open requests served per rolling
// second. Enough headroom for legitimate bursts (e.g. a CLI opening a few
// URLs back-to-back) while still containing a buggy or hostile loop.
const rateLimit = 10

// rateBurst is the token bucket burst capacity layered on top of rateLimit.
const rateBurst = 5

// DaemonOptions tunes a Run call. Zero values are valid for the production
// path; tests override Listener to inject a pre-bound listener.
type DaemonOptions struct {
	// Preferred is the port to try first; if 0, DefaultPort is used.
	Preferred int
	// Listener, if non-nil, is used in place of BindListener. Tests use this
	// to bind to an ephemeral port without exercising the fallback logic.
	Listener net.Listener
	// Now overrides the wall clock used by the rate limiter. Tests inject a
	// fake clock; production callers leave it nil to use time.Now.
	Now func() time.Time
	// Open invokes the host's default URL handler. Tests override; production
	// callers leave it nil to use hostOpenCommand.
	Open func(ctx context.Context, url string) error
}

// Run starts the browser-bridge HTTP server in the foreground. It returns
// only when ctx is cancelled or the listener fails. Intended to be invoked
// by the LaunchAgent / systemd unit; `toolbox browser-bridge daemon` is a
// thin cobra wrapper.
func Run(ctx context.Context, opts DaemonOptions) error {
	state, err := ResolveHostState()
	if err != nil {
		return err
	}
	if err := EnsureHostDir(state); err != nil {
		return err
	}

	token, err := LoadOrCreateToken(state)
	if err != nil {
		return err
	}

	ln := opts.Listener
	port := 0
	if ln == nil {
		preferred := opts.Preferred
		if preferred == 0 {
			preferred = DefaultPort
		}
		ln, port, err = BindListener(preferred)
		if err != nil {
			return err
		}
	} else {
		if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
			port = tcpAddr.Port
		}
	}
	defer func() { _ = ln.Close() }()

	if err := WritePort(state, port); err != nil {
		return err
	}
	defer func() { _ = ClearPort(state) }()

	if err := writePIDFile(state.PID); err != nil {
		return err
	}
	defer func() { _ = os.Remove(state.PID) }()

	logger, closeLog, err := openLogger(state.Log)
	if err != nil {
		return err
	}
	defer closeLog()

	logger.Printf("daemon: listening on 127.0.0.1:%d (toolbox %s)", port, runtime.GOOS)

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	openFn := opts.Open
	if openFn == nil {
		openFn = hostOpenCommand
	}

	handler := newHandler(token, openFn, logger, now)

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		logger.Printf("daemon: shutdown via context")
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http serve: %w", err)
	}
}

// handler is the single HTTP handler the daemon mounts on /open. Extracted
// so tests can drive it through net/http/httptest without re-binding a real
// socket on every case.
type handler struct {
	token   string
	openFn  func(ctx context.Context, url string) error
	logger  *log.Logger
	limiter *rateLimiter
}

func newHandler(token string, openFn func(ctx context.Context, url string) error, logger *log.Logger, now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	h := &handler{
		token:   token,
		openFn:  openFn,
		logger:  logger,
		limiter: newRateLimiter(rateLimit, rateBurst, now),
	}
	mux.HandleFunc("/open", h.handleOpen)
	mux.HandleFunc("/healthz", h.handleHealth)
	return mux
}

func (h *handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
}

// openRequest is the body shape the wrapper POSTs to /open.
type openRequest struct {
	URL string `json:"url"`
}

func (h *handler) handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authOK(r) {
		h.logger.Printf("open: rejected (bad token) from %s", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.limiter.Allow() {
		h.logger.Printf("open: rejected (rate limited) from %s", r.RemoteAddr)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxURLLen+1024))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var req openRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "malformed json", http.StatusBadRequest)
		return
	}
	clean, err := ValidateURL(req.URL)
	if err != nil {
		h.logger.Printf("open: rejected (%v) url=%q", err, truncate(req.URL, 256))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := h.openFn(ctx, clean); err != nil {
		h.logger.Printf("open: handler failed: %v url=%q", err, truncate(clean, 256))
		http.Error(w, "open handler failed", http.StatusBadGateway)
		return
	}
	h.logger.Printf("open: ok url=%q", truncate(clean, 256))
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) authOK(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(got) <= len(prefix) || got[:len(prefix)] != prefix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got[len(prefix):]), []byte(h.token)) == 1
}

// hostOpenCommand dispatches to the platform's URL handler. macOS exposes
// /usr/bin/open which respects the default-browser preference; Linux uses
// xdg-open from xdg-utils. Other platforms surface an error rather than
// guessing.
func hostOpenCommand(ctx context.Context, url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "/usr/bin/open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	default:
		return fmt.Errorf("browser-bridge: unsupported host OS %q", runtime.GOOS)
	}
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", cmd.Path, err)
	}
	return nil
}

// openLogger opens (or creates) the audit log in append mode and returns a
// log.Logger plus a close func. Logs are kept simple (timestamped lines) so
// `tail -f ~/.toolbox/browser/log` is the supported diagnostic path.
func openLogger(path string) (*log.Logger, func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open log %s: %w", path, err)
	}
	l := log.New(f, "", log.LstdFlags|log.LUTC)
	return l, func() { _ = f.Close() }, nil
}

func writePIDFile(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// rateLimiter is a tiny token bucket. Standard library has no built-in;
// pulling in golang.org/x/time/rate just for one endpoint is overkill.
type rateLimiter struct {
	mu        sync.Mutex
	tokens    float64
	maxTokens float64
	refill    float64 // tokens per second
	last      time.Time
	now       func() time.Time
}

func newRateLimiter(rate, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		tokens:    float64(burst),
		maxTokens: float64(burst),
		refill:    float64(rate),
		last:      now(),
		now:       now,
	}
}

func (l *rateLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	elapsed := now.Sub(l.last).Seconds()
	if elapsed > 0 {
		l.tokens += elapsed * l.refill
		if l.tokens > l.maxTokens {
			l.tokens = l.maxTokens
		}
		l.last = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}
