package bridge

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
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
	"strings"
	"sync"
	"time"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// requestTimeout caps how long the daemon waits for the URL handler to
// return. /usr/bin/open and xdg-open both fork+exit quickly, so anything
// past a few seconds means the host environment is wedged; we surface that
// to the wrapper instead of hanging the in-container CLI.
const requestTimeout = 5 * time.Second

// rateBudget is one token bucket's allowance: the sustained requests per
// rolling second, plus the burst layered on top. The two are meaningless apart
// — a rate with no burst throttles the first legitimate pair of requests — so
// they travel as one value, and each route's budget is named once below.
type rateBudget struct {
	perSecond int
	burst     int
}

// sharedBudget is the one bucket /open, /edit and /proximo draw from. Enough
// headroom for legitimate bursts (a CLI opening a few URLs back-to-back) while
// still containing a buggy or hostile loop.
var sharedBudget = rateBudget{perSecond: 10, burst: 5}

// credBudget gives /credential its own, more generous bucket. A single `git
// clone` with many HTTPS submodules fires a rapid burst of credential lookups
// (each submodule is a separate git process → get + store); sharing
// sharedBudget would 429 them and break the clone. Still bounded so a runaway
// loop can't hammer the host credential store unchecked.
var credBudget = rateBudget{perSecond: 30, burst: 15}

// soundBudget gives /sound its own bucket too. Same shape as sharedBudget, and
// that is the whole point: separation, not headroom — a run of chimes must not
// spend the budget an OAuth redirect on /open needs, and vice versa. Unlike
// /credential this route earns no extra room; herdr brakes upstream
// (ui.toast.delay_seconds, and a new state on a pane cancels that pane's
// pending notification), so a burst here is short by construction.
var soundBudget = rateBudget{perSecond: 10, burst: 5}

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
	// Proximo executes an allowlisted proximo subcommand on the host, with the
	// request's arguments appended. Tests override; production callers leave it
	// nil to use launchProximo.
	Proximo func(ctx context.Context, command string, args []string, agent proximoAgentHome) (output []byte, exit int, err error)
	// Credential forwards an allowlisted git credential operation to the host
	// git. Tests override; production callers leave it nil to use
	// runHostCredential.
	Credential func(ctx context.Context, op string, input []byte) (output []byte, exit int, err error)
	// Sound plays an MP3 payload on the host. It takes no context: the player
	// outlives the request by design (fire-and-forget), so its deadline is the
	// implementation's own. Tests override; production callers leave it nil to
	// use playSound.
	Sound func(data []byte) error
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

	ln, port, err := resolveListener(opts)
	if err != nil {
		return err
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
	fns := handlerFns{open: opts.Open, edit: opts.Edit, proximo: opts.Proximo, credential: opts.Credential, sound: opts.Sound}

	srv := &http.Server{
		Handler:           newHandler(token, fns.withHostDefaults(), logger, now),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	listeners := []net.Listener{ln}
	if unixLn != nil {
		listeners = append(listeners, unixLn)
	}
	return serve(ctx, srv, logger, listeners...)
}

// resolveListener returns the TCP listener to serve on plus the port it holds.
// A caller-supplied listener (tests) is used as-is; otherwise the preferred
// port is bound, falling back to DefaultPort when unset.
func resolveListener(opts DaemonOptions) (net.Listener, int, error) {
	if opts.Listener == nil {
		preferred := opts.Preferred
		if preferred == 0 {
			preferred = DefaultPort
		}
		return BindListener(preferred)
	}
	if tcpAddr, ok := opts.Listener.Addr().(*net.TCPAddr); ok {
		return opts.Listener, tcpAddr.Port, nil
	}
	return opts.Listener, 0, nil
}

// withHostDefaults fills every unset callback with its production
// implementation, so a test overrides only the endpoint it exercises.
func (f handlerFns) withHostDefaults() handlerFns {
	if f.open == nil {
		f.open = hostOpenCommand
	}
	if f.edit == nil {
		f.edit = launchEditor
	}
	if f.proximo == nil {
		f.proximo = launchProximo
	}
	if f.credential == nil {
		f.credential = runHostCredential
	}
	if f.sound == nil {
		f.sound = playSound
	}
	return f
}

// serve runs srv on every listener and blocks until ctx is cancelled or one of
// them fails. The error channel is buffered per listener: every Serve goroutine
// must be able to send after a shutdown, or the ones that lose the race leak.
func serve(ctx context.Context, srv *http.Server, logger *log.Logger, listeners ...net.Listener) error {
	serveErr := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func() { serveErr <- srv.Serve(ln) }()
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
	open       func(ctx context.Context, url string) error
	edit       func(ctx context.Context, editor, path string) error
	proximo    func(ctx context.Context, command string, args []string, agent proximoAgentHome) (output []byte, exit int, err error)
	credential func(ctx context.Context, op string, input []byte) (output []byte, exit int, err error)
	sound      func(data []byte) error
}

// handler is the single HTTP handler the daemon mounts its endpoints on.
// Extracted so tests can drive it through net/http/httptest without
// re-binding a real socket on every case.
type handler struct {
	token        string
	fns          handlerFns
	logger       *log.Logger
	limiter      *rateLimiter
	credLimiter  *rateLimiter
	soundLimiter *rateLimiter
}

func newHandler(token string, fns handlerFns, logger *log.Logger, now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	h := &handler{
		token:        token,
		fns:          fns,
		logger:       logger,
		limiter:      newRateLimiter(sharedBudget, now),
		credLimiter:  newRateLimiter(credBudget, now),
		soundLimiter: newRateLimiter(soundBudget, now),
	}
	mux.HandleFunc(RouteOpen, h.handleOpen)
	mux.HandleFunc(RouteEdit, h.handleEdit)
	mux.HandleFunc(RouteProximo, h.handleProximo)
	mux.HandleFunc(RouteCredential, h.handleCredential)
	mux.HandleFunc(RouteSound, h.handleSound)
	mux.HandleFunc(RouteHealth, h.handleHealth)
	return mux
}

// writeJSONOK sends v as a 200 JSON response. Every success path on this
// daemon has the same three lines; net/http ships no constant for the header,
// so the shape lives here once.
func writeJSONOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSONOK(w, map[string]string{"status": "ok"})
}

// openRequest is the body shape the wrapper POSTs to /open.
type openRequest struct {
	URL string `json:"url"`
}

// decodeJSON reads at most limit bytes of r's body into dst, writing the 400
// response itself on failure — the shared prologue of every POST handler.
func (h *handler) decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	// limit+1: io.LimitReader truncates rather than erroring, so reading one
	// byte past the cap is what tells an over-long body apart from a body that
	// merely fills it — otherwise the truncated JSON fails to parse and the
	// caller is told its JSON is malformed when the real problem is size.
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return false
	}
	if int64(len(body)) > limit {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		http.Error(w, "malformed json", http.StatusBadRequest)
		return false
	}
	return true
}

// gate applies the shared request gates (POST only, bearer token, rate
// limit) for /open, /edit and /proximo against the shared bucket. Returns
// false after writing the error response; verb labels the audit-log lines.
func (h *handler) gate(verb string, w http.ResponseWriter, r *http.Request) bool {
	return h.gateLimited(verb, h.limiter, w, r)
}

// gateLimited is gate parameterised by the token bucket, so /credential can
// gate against its own (more generous) limiter instead of the shared one.
func (h *handler) gateLimited(verb string, limiter *rateLimiter, w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !h.authOK(r) {
		h.logger.Printf("%s: rejected (bad token) from %s", verb, r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if !limiter.Allow() {
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
// command must match proximoAllowlist verbatim; Args is the rest of the
// container-side argv, forwarded verbatim to the host binary. The shim knows
// nothing about proximo's flags — classification lives here, so a flag added
// upstream needs no toolbox change.
type proximoRequest struct {
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	Home      string   `json:"home"`
	CodexHome string   `json:"codex_home"`
}

// proximoBodyLimit caps a /proximo request body. Four times the other routes'
// 4KiB because this one carries argv: a handful of long paths is ordinary,
// while anything approaching this is not a command line a human wrote.
const proximoBodyLimit = 16384

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
	if !h.decodeJSON(w, r, proximoBodyLimit, &req) {
		return
	}
	if _, ok := proximoAllowlist[req.Command]; !ok {
		h.logger.Printf("proximo: rejected (command not allowed) command=%q", truncate(req.Command, 64))
		http.Error(w, "command not allowed — only "+strings.Join(AllowedProximoCommands(), ", ")+" are bridged; run anything else on the host", http.StatusBadRequest)
		return
	}
	if isProximoHostWrite(req.Command, req.Args) {
		h.logger.Printf("proximo: rejected (writes a host file) command=%q", req.Command)
		http.Error(w, "errors dom is not bridged — it always writes an HTML file on the host, where this container cannot read it; run it on the host", http.StatusBadRequest)
		return
	}
	for _, arg := range req.Args {
		if isProximoOutputFlag(arg) {
			h.logger.Printf("proximo: rejected (output flag) command=%q arg=%q", req.Command, truncate(arg, 64))
			http.Error(w, "-o/--output is not bridged — it would write to the host filesystem; redirect the output in the container instead", http.StatusBadRequest)
			return
		}
	}
	// The exec can legitimately run for minutes (first `up` pulls the stack
	// images), far past the server's WriteTimeout — push the connection's
	// write deadline beyond the execution budget for this response only.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(proximoTimeout + 10*time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), proximoTimeout)
	defer cancel()
	out, exit, err := h.fns.proximo(ctx, req.Command, req.Args, proximoAgentHome{Home: req.Home, CodexHome: req.CodexHome})
	if err != nil {
		h.logger.Printf("proximo: handler failed: %v command=%q", err, req.Command)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.logger.Printf("proximo: ok command=%q exit=%d", req.Command, exit)
	writeJSONOK(w, proximoResponse{Exit: exit, Output: string(out)})
}

// credentialTimeout bounds a /credential execution. Above the shared 5s
// requestTimeout because a first-time `store` can raise a macOS Keychain (or
// Linux secret-service) authorization dialog the user must click; a `get`
// against an already-authorized item returns in milliseconds.
const credentialTimeout = 60 * time.Second

// maxCredentialBody caps the /credential request body. A git credential
// exchange is a handful of short key=value lines; 64 KiB is generous headroom.
const maxCredentialBody = 64 << 10

// credentialRequest is the body shape the git-credential-toolbox shim POSTs to
// /credential. Op is the git helper operation (get|store|erase); Input is the
// raw credential protocol text git wrote on the shim's stdin.
type credentialRequest struct {
	Op    string `json:"op"`
	Input string `json:"input"`
}

// credentialResponse carries git's stdout and exit code back to the shim,
// which writes the output to git and propagates the exit.
type credentialResponse struct {
	Exit   int    `json:"exit"`
	Output string `json:"output"`
}

func (h *handler) handleCredential(w http.ResponseWriter, r *http.Request) {
	if !h.gateLimited("credential", h.credLimiter, w, r) {
		return
	}
	var req credentialRequest
	if !h.decodeJSON(w, r, maxCredentialBody, &req) {
		return
	}
	if _, ok := credentialSubcommand[req.Op]; !ok {
		h.logger.Printf("credential: rejected (op not allowed) op=%q", truncate(req.Op, 64))
		http.Error(w, "op not allowed — only get, store, erase are bridged", http.StatusBadRequest)
		return
	}
	// A first-time `store` may block on a host keychain authorization dialog,
	// past the server's WriteTimeout — push the write deadline for this
	// response only, mirroring /proximo.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(credentialTimeout + 10*time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), credentialTimeout)
	defer cancel()
	out, exit, err := h.fns.credential(ctx, req.Op, []byte(req.Input))
	if err != nil {
		h.logger.Printf("credential: handler failed: %v op=%q", err, req.Op)
		http.Error(w, "credential handler failed", http.StatusBadGateway)
		return
	}
	// Never log the exchange body — it carries secrets. Op + exit only.
	h.logger.Printf("credential: ok op=%q exit=%d", req.Op, exit)
	writeJSONOK(w, credentialResponse{Exit: exit, Output: string(out)})
}

// maxSoundBody caps a /sound request body at 512 KiB. herdr's built-in chimes
// are tens of kilobytes, so roughly a third of that base64-encoded; the cap is
// sized for a developer's own ui.sound.*_path override rather than for the
// smallest payload observed, because a tighter one would be discovered as a
// 413 from a daemon nobody is watching.
const maxSoundBody = 512 << 10

// soundRequest is the body the paplay shim POSTs to /sound. Data is the MP3
// content, base64-encoded: the container temp file herdr wrote is unreadable
// from the host, so the bytes travel and the daemon picks the path itself
// (ADR-0009). Name is the container-side basename, carried for the audit log
// alone and never used to build a path.
type soundRequest struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

func (h *handler) handleSound(w http.ResponseWriter, r *http.Request) {
	if !h.gateLimited("sound", h.soundLimiter, w, r) {
		return
	}
	var req soundRequest
	if !h.decodeJSON(w, r, maxSoundBody, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil || len(data) == 0 {
		h.logger.Printf("sound: rejected (payload not non-empty base64) name=%q", truncate(req.Name, 128))
		http.Error(w, "sound payload must be non-empty base64", http.StatusBadRequest)
		return
	}
	// Fire-and-forget: the player is spawned detached and the 200 goes out
	// now. Waiting for playback would block herdr's client for the length of
	// the chime and queue two completions moments apart, where overlapping
	// them is what herdr does natively on a host.
	if err := h.fns.sound(data); err != nil {
		h.logger.Printf("sound: handler failed: %v name=%q bytes=%d", err, truncate(req.Name, 128), len(data))
		http.Error(w, "sound handler failed", http.StatusBadGateway)
		return
	}
	// Follows the /credential discipline: the verb, the size and the file,
	// never the bytes. The name is herdr's own temp-file basename
	// (herdr-sound-<pid>-<n>.mp3), so it correlates a line here with a line in
	// herdr-client.log; which of the two chimes it was stays herdr's secret,
	// the payload carrying no such field.
	h.logger.Printf("sound: ok name=%q bytes=%d", truncate(req.Name, 128), len(data))
	w.WriteHeader(http.StatusOK)
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
// for the URL handler and the editor launchers. Direct exec, no shell. stderr
// is captured rather than discarded: a bare "exit status 1" from
// /usr/bin/open hides the only useful part ("Unable to find application named
// …"), which the audit log is the sole place to read.
func runQuiet(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if reason := strings.TrimSpace(stderr.String()); reason != "" {
			return fmt.Errorf("run %s: %w: %s", cmd.Path, err, reason)
		}
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
		noop := func() {
			// The open failed, so there is no file to close — callers can
			// still invoke the returned func unconditionally.
		}
		return nil, noop, fmt.Errorf("open log %s: %w", path, err)
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

func newRateLimiter(b rateBudget, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		tokens:    float64(b.burst),
		maxTokens: float64(b.burst),
		refill:    float64(b.perSecond),
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
