package bridge

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
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// requestTimeout caps how long the daemon waits for the URL handler to
// return. /usr/bin/open and xdg-open both fork+exit quickly, so anything
// past a few seconds means the host environment is wedged; we surface that
// to the wrapper instead of hanging the in-container CLI.
const requestTimeout = 5 * time.Second

// rateLimit is the maximum number of /open + /edit requests served per
// rolling second (one shared bucket). Enough headroom for legitimate bursts
// (e.g. a CLI opening a few URLs back-to-back) while still containing a
// buggy or hostile loop.
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
	// Edit launches a host editor on a path. Tests override; production
	// callers leave it nil to use the per-OS launchEditor.
	Edit func(ctx context.Context, editor, path string) error
	// Proximo executes an allowlisted proximo subcommand on the host. Tests
	// override; production callers leave it nil to use launchProximo.
	Proximo func(ctx context.Context, command string) (output []byte, exit int, err error)
}

// Run starts the bridge HTTP server in the foreground. It returns
// only when ctx is cancelled or the listener fails. Intended to be invoked
// by the LaunchAgent / systemd unit; `toolbox bridge daemon` is a
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

	// Linux-only second transport: native docker-ce containers cannot reach
	// the loopback TCP listener (host-gateway resolves to the docker0 gateway
	// IP), so they connect through this socket via the bridge-run RW mount.
	// Failure to bind (e.g. $HOME beyond sun_path's ~108-char limit) degrades
	// to TCP-only instead of crash-looping the systemd unit.
	unixLn, err := bindUnixListener(state)
	if err != nil {
		logger.Printf("daemon: unix socket unavailable (%v) — TCP only", err)
	}
	if unixLn != nil {
		defer func() {
			_ = unixLn.Close()
			_ = os.Remove(state.Socket)
		}()
		logger.Printf("daemon: listening on unix %s", state.Socket)
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	fns := handlerFns{open: opts.Open, edit: opts.Edit, proximo: opts.Proximo}
	if fns.open == nil {
		fns.open = hostOpenCommand
	}
	if fns.edit == nil {
		fns.edit = launchEditor
	}
	if fns.proximo == nil {
		fns.proximo = launchProximo
	}

	handler := newHandler(token, fns, logger, now)

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	// Buffer 2: both Serve goroutines must be able to send after a shutdown
	// or the second one leaks.
	serveErr := make(chan error, 2)
	go func() {
		serveErr <- srv.Serve(ln)
	}()
	if unixLn != nil {
		go func() {
			serveErr <- srv.Serve(unixLn)
		}()
	}

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

// handlerFns bundles the resolved host-action callbacks, one field per
// endpoint — adding an endpoint adds a field here instead of another
// positional parameter on newHandler (and lets tests override only the
// field they exercise).
type handlerFns struct {
	open    func(ctx context.Context, url string) error
	edit    func(ctx context.Context, editor, path string) error
	proximo func(ctx context.Context, command string) (output []byte, exit int, err error)
}

// handler is the single HTTP handler the daemon mounts its endpoints on.
// Extracted so tests can drive it through net/http/httptest without
// re-binding a real socket on every case.
type handler struct {
	token   string
	fns     handlerFns
	logger  *log.Logger
	limiter *rateLimiter
}

func newHandler(token string, fns handlerFns, logger *log.Logger, now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	h := &handler{
		token:   token,
		fns:     fns,
		logger:  logger,
		limiter: newRateLimiter(rateLimit, rateBurst, now),
	}
	mux.HandleFunc("/open", h.handleOpen)
	mux.HandleFunc("/edit", h.handleEdit)
	mux.HandleFunc("/proximo", h.handleProximo)
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

// decodeJSON reads at most limit bytes of r's body into dst, writing the 400
// response itself on failure — the shared prologue of every POST handler.
func (h *handler) decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		http.Error(w, "malformed json", http.StatusBadRequest)
		return false
	}
	return true
}

// gate applies the shared request gates (POST only, bearer token, rate
// limit) for /open and /edit. Returns false after writing the error
// response; verb labels the audit-log lines.
func (h *handler) gate(verb string, w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !h.authOK(r) {
		h.logger.Printf("%s: rejected (bad token) from %s", verb, r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if !h.limiter.Allow() {
		h.logger.Printf("%s: rejected (rate limited) from %s", verb, r.RemoteAddr)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return false
	}
	return true
}

func (h *handler) handleOpen(w http.ResponseWriter, r *http.Request) {
	if !h.gate("open", w, r) {
		return
	}
	var req openRequest
	if !h.decodeJSON(w, r, MaxURLLen+1024, &req) {
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
	if err := h.fns.open(ctx, clean); err != nil {
		h.logger.Printf("open: handler failed: %v url=%q", err, truncate(clean, 256))
		http.Error(w, "open handler failed", http.StatusBadGateway)
		return
	}
	h.logger.Printf("open: ok url=%q", truncate(clean, 256))
	w.WriteHeader(http.StatusNoContent)
}

// editRequest is the body shape the editor shims POST to /edit.
type editRequest struct {
	Editor string `json:"editor"`
	Path   string `json:"path"`
}

func (h *handler) handleEdit(w http.ResponseWriter, r *http.Request) {
	if !h.gate("edit", w, r) {
		return
	}
	// Reuse the /open body cap: host paths sit far below MaxURLLen.
	var req editRequest
	if !h.decodeJSON(w, r, MaxURLLen+1024, &req) {
		return
	}
	if _, ok := editorAllowlist[req.Editor]; !ok {
		h.logger.Printf("edit: rejected (unknown editor) editor=%q", truncate(req.Editor, 64))
		http.Error(w, "unknown editor", http.StatusBadRequest)
		return
	}
	clean := filepath.Clean(req.Path)
	if !filepath.IsAbs(clean) {
		h.logger.Printf("edit: rejected (relative path) path=%q", truncate(req.Path, 256))
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(clean); err != nil {
		h.logger.Printf("edit: rejected (stat: %v) path=%q", err, truncate(clean, 256))
		http.Error(w, "path does not exist on host", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := h.fns.edit(ctx, req.Editor, clean); err != nil {
		h.logger.Printf("edit: handler failed: %v editor=%q path=%q", err, req.Editor, truncate(clean, 256))
		http.Error(w, "edit handler failed", http.StatusBadGateway)
		return
	}
	h.logger.Printf("edit: ok editor=%q path=%q", req.Editor, truncate(clean, 256))
	w.WriteHeader(http.StatusNoContent)
}

// proximoRequest is the body shape the proximo shim POSTs to /proximo. The
// command must match proximoAllowlist verbatim — no arguments.
type proximoRequest struct {
	Command string `json:"command"`
}

// proximoResponse carries the host command's combined output and exit code
// back to the shim, which prints the output and propagates the exit.
type proximoResponse struct {
	Exit   int    `json:"exit"`
	Output string `json:"output"`
}

func (h *handler) handleProximo(w http.ResponseWriter, r *http.Request) {
	if !h.gate("proximo", w, r) {
		return
	}
	var req proximoRequest
	if !h.decodeJSON(w, r, 4096, &req) {
		return
	}
	if _, ok := proximoAllowlist[req.Command]; !ok {
		h.logger.Printf("proximo: rejected (command not allowed) command=%q", truncate(req.Command, 64))
		http.Error(w, "command not allowed — only up, down, status are bridged; run anything else on the host", http.StatusBadRequest)
		return
	}
	// The exec can legitimately run for minutes (first `up` pulls the stack
	// images), far past the server's WriteTimeout — push the connection's
	// write deadline beyond the execution budget for this response only.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(proximoTimeout + 10*time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), proximoTimeout)
	defer cancel()
	out, exit, err := h.fns.proximo(ctx, req.Command)
	if err != nil {
		h.logger.Printf("proximo: handler failed: %v command=%q", err, req.Command)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.logger.Printf("proximo: ok command=%q exit=%d", req.Command, exit)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(proximoResponse{Exit: exit, Output: string(out)})
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
	switch runtime.GOOS {
	case "darwin":
		return runQuiet(ctx, "/usr/bin/open", url)
	case "linux":
		return runQuiet(ctx, "xdg-open", url)
	default:
		return fmt.Errorf("bridge: unsupported host OS %q", runtime.GOOS)
	}
}

// runQuiet execs name with args detached from stdio — shared launch plumbing
// for the URL handler and the editor launchers. Direct exec, no shell.
func runQuiet(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
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
// `tail -f ~/.toolbox/toolbox/bridge/log` is the supported diagnostic path.
func openLogger(path string) (*log.Logger, func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open log %s: %w", path, err)
	}
	l := log.New(f, "", log.LstdFlags|log.LUTC)
	return l, func() { _ = f.Close() }, nil
}

func writePIDFile(path string) error {
	return fsx.AtomicWriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
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
