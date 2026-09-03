package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// buildTestHandler wires fns into a handler with the shared test fixture
// (token "tok", discarded logger, fixed clock). Zero-value fields get inert
// implementations, so each test sets only the fn it exercises.
func buildTestHandler(t *testing.T, fns handlerFns) http.Handler {
	t.Helper()
	if fns.open == nil {
		fns.open = func(_ context.Context, _ string) error { return nil }
	}
	if fns.edit == nil {
		fns.edit = func(_ context.Context, _, _ string) error { return nil }
	}
	if fns.proximo == nil {
		fns.proximo = func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
			return nil, 0, nil
		}
	}
	if fns.credential == nil {
		fns.credential = func(_ context.Context, _ string, _ []byte) ([]byte, int, error) { return nil, 0, nil }
	}
	if fns.sound == nil {
		fns.sound = func(_ []byte) error { return nil }
	}
	var logBuf strings.Builder
	logger := log.New(&logBuf, "", 0)
	now := func() time.Time { return time.Unix(0, 0) }
	return newHandler("tok", fns, logger, now)
}

func newTestHandler(t *testing.T, openErr error) (http.Handler, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var openCalls, editCalls atomic.Int32
	h := buildTestHandler(t, handlerFns{
		open: func(_ context.Context, _ string) error {
			openCalls.Add(1)
			return openErr
		},
		edit: func(_ context.Context, _, _ string) error {
			editCalls.Add(1)
			return nil
		},
	})
	return h, &openCalls, &editCalls
}

// newProximoTestHandler builds a handler whose /proximo executor is the given
// fake; open/edit are inert.
func newProximoTestHandler(t *testing.T, fn func(ctx context.Context, command string, args []string, agent proximoAgentHome) ([]byte, int, error)) http.Handler {
	t.Helper()
	return buildTestHandler(t, handlerFns{proximo: fn})
}

func doPost(t *testing.T, h http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doPostTo(t, h, RouteOpen, token, body)
}

func doPostTo(t *testing.T, h http.Handler, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandler_OpenOK(t *testing.T) {
	h, calls, _ := newTestHandler(t, nil)
	rr := doPost(t, h, "tok", `{"url":"https://example.com"}`)
	if rr.Code != http.StatusNoContent {
		t.Errorf("code = %d, body=%q", rr.Code, rr.Body.String())
	}
	if calls.Load() != 1 {
		t.Errorf("openFn calls = %d", calls.Load())
	}
}

func TestHandler_RejectBadMethod(t *testing.T) {
	h, _, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, RouteOpen, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d", rr.Code)
	}
}

func TestHandler_RejectBadToken(t *testing.T) {
	h, calls, _ := newTestHandler(t, nil)
	rr := doPost(t, h, "wrong", `{"url":"https://example.com"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("code = %d", rr.Code)
	}
	if calls.Load() != 0 {
		t.Errorf("openFn should not be called on bad token")
	}
}

func TestHandler_RejectBadURL(t *testing.T) {
	h, calls, _ := newTestHandler(t, nil)
	rr := doPost(t, h, "tok", `{"url":"file:///etc/passwd"}`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rr.Code)
	}
	if calls.Load() != 0 {
		t.Errorf("openFn should not be called on bad URL")
	}
}

func TestHandler_RateLimit(t *testing.T) {
	h, _, _ := newTestHandler(t, nil)
	for i := range 5 {
		rr := doPost(t, h, "tok", `{"url":"https://example.com"}`)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("burst[%d] code = %d", i, rr.Code)
		}
	}
	rr := doPost(t, h, "tok", `{"url":"https://example.com"}`)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("post-burst code = %d, want 429", rr.Code)
	}
}

func TestHandler_OpenFails(t *testing.T) {
	h, _, _ := newTestHandler(t, io.EOF)
	rr := doPost(t, h, "tok", `{"url":"https://example.com"}`)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("code = %d", rr.Code)
	}
}

func editBody(editor, path string) string {
	b, _ := json.Marshal(map[string]string{"editor": editor, "path": path})
	return string(b)
}

func TestHandler_EditOK(t *testing.T) {
	h, _, editCalls := newTestHandler(t, nil)
	rr := doPostTo(t, h, RouteEdit, "tok", editBody("code", t.TempDir()))
	if rr.Code != http.StatusNoContent {
		t.Errorf("code = %d, body=%q", rr.Code, rr.Body.String())
	}
	if editCalls.Load() != 1 {
		t.Errorf("editFn calls = %d", editCalls.Load())
	}
}

func TestHandler_EditRejectUnknownEditor(t *testing.T) {
	h, _, editCalls := newTestHandler(t, nil)
	rr := doPostTo(t, h, RouteEdit, "tok", editBody("vim; rm -rf /", t.TempDir()))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rr.Code)
	}
	if editCalls.Load() != 0 {
		t.Errorf("editFn should not be called on unknown editor")
	}
}

func TestHandler_EditRejectMissingPath(t *testing.T) {
	h, _, editCalls := newTestHandler(t, nil)
	rr := doPostTo(t, h, RouteEdit, "tok", editBody("code", "/nope/missing"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rr.Code)
	}
	if editCalls.Load() != 0 {
		t.Errorf("editFn should not be called on missing path")
	}
}

func TestHandler_EditRejectRelativePath(t *testing.T) {
	h, _, editCalls := newTestHandler(t, nil)
	rr := doPostTo(t, h, RouteEdit, "tok", editBody("code", "relative/path"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rr.Code)
	}
	if editCalls.Load() != 0 {
		t.Errorf("editFn should not be called on relative path")
	}
}

func TestHandler_EditRejectBadToken(t *testing.T) {
	h, _, editCalls := newTestHandler(t, nil)
	for _, token := range []string{"", "wrong"} {
		rr := doPostTo(t, h, RouteEdit, token, editBody("code", t.TempDir()))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("token=%q code = %d", token, rr.Code)
		}
	}
	if editCalls.Load() != 0 {
		t.Errorf("editFn should not be called on bad token")
	}
}

func TestHandler_EditSharesRateLimitWithOpen(t *testing.T) {
	h, _, _ := newTestHandler(t, nil)
	for i := range 5 {
		rr := doPost(t, h, "tok", `{"url":"https://example.com"}`)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("burst[%d] code = %d", i, rr.Code)
		}
	}
	rr := doPostTo(t, h, RouteEdit, "tok", editBody("code", t.TempDir()))
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("post-burst /edit code = %d, want 429 (shared limiter)", rr.Code)
	}
}

func proximoBody(command string, args ...string) string {
	b, _ := json.Marshal(map[string]any{"command": command, "args": args})
	return string(b)
}

func TestHandler_ProximoForwardsArgs(t *testing.T) {
	var gotArgs []string
	h := newProximoTestHandler(t, func(_ context.Context, _ string, args []string, _ proximoAgentHome) ([]byte, int, error) {
		gotArgs = args
		return nil, 0, nil
	})
	rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("status", "--json", "--service", "web"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%q", rr.Code, rr.Body.String())
	}
	if want := []string{"--json", "--service", "web"}; !slices.Equal(gotArgs, want) {
		t.Errorf("args = %q, want %q", gotArgs, want)
	}
}

func TestHandler_ProximoOK(t *testing.T) {
	var gotCmd string
	h := newProximoTestHandler(t, func(_ context.Context, command string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
		gotCmd = command
		return []byte("stack started\n"), 0, nil
	})
	rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("up"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%q", rr.Code, rr.Body.String())
	}
	if gotCmd != "up" {
		t.Errorf("command = %q", gotCmd)
	}
	var resp proximoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, rr.Body.String())
	}
	if resp.Exit != 0 || resp.Output != "stack started\n" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestHandler_ProximoPropagatesExitCode(t *testing.T) {
	h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
		return []byte("compose failed\n"), 3, nil
	})
	rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("up"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d", rr.Code)
	}
	var resp proximoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Exit != 3 {
		t.Errorf("exit = %d, want 3", resp.Exit)
	}
}

// TestHandler_ProximoAllowsBridgedVerbs pins the verb gate as a whole: the
// three lifecycle verbs plus the two that make the agent-facing loop work —
// `errors` reads the inspector's browser reports back, `skill` installs
// proximo's own agent skill where the container's agents look for it.
func TestHandler_ProximoAllowsBridgedVerbs(t *testing.T) {
	for _, cmd := range AllowedProximoCommands() {
		called := false
		h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
			called = true
			return nil, 0, nil
		})
		rr := doPostTo(t, h, RouteProximo, "tok", proximoBody(cmd))
		if rr.Code != http.StatusOK {
			t.Errorf("command %q: code = %d, want 200 (body=%q)", cmd, rr.Code, rr.Body.String())
		}
		if !called {
			t.Errorf("command %q: executor must run", cmd)
		}
	}
	for _, cmd := range []string{"errors", "skill"} {
		if !slices.Contains(AllowedProximoCommands(), cmd) {
			t.Errorf("%q must be bridged", cmd)
		}
	}
}

func TestHandler_ProximoRejectUnknownCommand(t *testing.T) {
	for _, cmd := range []string{"install", "uninstall", "config", "up --observability", ""} {
		called := false
		h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
			called = true
			return nil, 0, nil
		})
		rr := doPostTo(t, h, RouteProximo, "tok", proximoBody(cmd))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("command %q: code = %d, want 400", cmd, rr.Code)
		}
		if called {
			t.Errorf("command %q: executor must not run", cmd)
		}
	}
}

// TestHandler_ProximoRejectOutputFlag pins the one argument-shaped rule of the
// verb gate: -o/--output would write to the HOST filesystem through the
// bridge, so it never reaches exec.
// TestHandler_ProximoForwardsAgentHome pins the path that makes `skill`
// land where an in-container agent reads: the session's real agent homes
// travel from the shim to the executor, because mounts_root / --profile /
// inherit_host_auth move them and the daemon cannot derive them.
func TestHandler_ProximoForwardsAgentHome(t *testing.T) {
	home := t.TempDir()
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o700); err != nil {
		t.Fatal(err)
	}
	var got proximoAgentHome
	h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, agent proximoAgentHome) ([]byte, int, error) {
		got = agent
		return nil, 0, nil
	})
	body, _ := json.Marshal(map[string]any{"command": "skill", "args": []string{"install"}, "home": home, "codex_home": codex})
	rr := doPostTo(t, h, RouteProximo, "tok", string(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%q", rr.Code, rr.Body.String())
	}
	if got.Home != home || got.CodexHome != codex {
		t.Errorf("agent home = %+v, want {%q %q}", got, home, codex)
	}
}

func TestHandler_ProximoRejectOutputFlag(t *testing.T) {
	for _, args := range [][]string{{"transcript", "-o", "/etc/hosts"}, {"dom", "--output=/tmp/x"}, {"transcript", "-o/tmp/x"}} {
		called := false
		h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
			called = true
			return nil, 0, nil
		})
		rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("errors", args...))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("args %q: code = %d, want 400", args, rr.Code)
		}
		if called {
			t.Errorf("args %q: executor must not run", args)
		}
	}
}

// TestHandler_ProximoOversizedBodyIsExplicit: the body cap used to truncate
// silently, so an over-long argv failed json.Unmarshal and the caller was told
// its JSON was malformed. With argv passthrough the cap is reachable, so it
// has to name the real cause.
func TestHandler_ProximoOversizedBodyIsExplicit(t *testing.T) {
	called := false
	h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	})
	rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("status", strings.Repeat("x", proximoBodyLimit)))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("code = %d, want 413; body = %q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "malformed") {
		t.Errorf("body %q blames JSON for a size problem", rr.Body.String())
	}
	if called {
		t.Error("executor must not run")
	}
}

// TestHandler_ProximoRejectErrorsDom: `errors dom` writes a file on the HOST
// even with no flag at all — upstream defaults the destination to
// os.TempDir()/proximo-dom-<id>.html (proximo internal/cli/errors.go:216-221).
// Through the bridge that file lands where the container cannot read it, so
// the subcommand buys nothing here and only leaves a host write behind. The
// --out gate cannot see it; the verb gate can.
func TestHandler_ProximoRejectErrorsDom(t *testing.T) {
	for _, args := range [][]string{{"dom", "abc123"}, {"--json", "dom", "abc123"}} {
		called := false
		h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
			called = true
			return nil, 0, nil
		})
		rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("errors", args...))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("args %q: code = %d, want 400", args, rr.Code)
		}
		if called {
			t.Errorf("args %q: executor must not run", args)
		}
	}
	// The sibling that writes to stdout stays bridged.
	called := false
	h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	})
	if rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("errors", "transcript")); rr.Code != http.StatusOK || !called {
		t.Errorf("errors transcript: code = %d, called = %v — must stay bridged", rr.Code, called)
	}
}

func TestHandler_ProximoRejectBadToken(t *testing.T) {
	called := false
	h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	})
	for _, token := range []string{"", "wrong"} {
		rr := doPostTo(t, h, RouteProximo, token, proximoBody("up"))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("token=%q code = %d", token, rr.Code)
		}
	}
	if called {
		t.Error("executor must not run on bad token")
	}
}

func TestHandler_ProximoExecErrorIs502(t *testing.T) {
	h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
		return nil, 0, io.EOF
	})
	rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("status"))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("code = %d, want 502", rr.Code)
	}
}

func TestHandler_ProximoBudgetExceedsRequestTimeout(t *testing.T) {
	var deadline time.Time
	h := newProximoTestHandler(t, func(ctx context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
		deadline, _ = ctx.Deadline()
		return nil, 0, nil
	})
	before := time.Now()
	rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("up"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d", rr.Code)
	}
	if got := deadline.Sub(before); got < 60*time.Second {
		t.Errorf("execution budget = %v, want >= 60s (first `up` pulls images)", got)
	}
}

func TestHandler_ProximoSharesRateLimit(t *testing.T) {
	h := newProximoTestHandler(t, func(_ context.Context, _ string, _ []string, _ proximoAgentHome) ([]byte, int, error) {
		return nil, 0, nil
	})
	for i := range 5 {
		rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("status"))
		if rr.Code != http.StatusOK {
			t.Fatalf("burst[%d] code = %d", i, rr.Code)
		}
	}
	rr := doPostTo(t, h, RouteProximo, "tok", proximoBody("status"))
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("post-burst code = %d, want 429 (shared limiter)", rr.Code)
	}
}

// newCredentialTestHandler builds a handler whose /credential executor is the
// given fake; open/edit/proximo are inert.
func newCredentialTestHandler(t *testing.T, fn func(ctx context.Context, op string, input []byte) ([]byte, int, error)) http.Handler {
	t.Helper()
	return buildTestHandler(t, handlerFns{credential: fn})
}

func credentialBody(op, input string) string {
	b, _ := json.Marshal(map[string]string{"op": op, "input": input})
	return string(b)
}

func TestHandler_CredentialOK(t *testing.T) {
	var gotOp string
	var gotInput []byte
	h := newCredentialTestHandler(t, func(_ context.Context, op string, input []byte) ([]byte, int, error) {
		gotOp = op
		gotInput = input
		return []byte("username=me\npassword=secret\n"), 0, nil
	})
	rr := doPostTo(t, h, RouteCredential, "tok", credentialBody("get", "protocol=https\nhost=forgejo.example\n\n"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%q", rr.Code, rr.Body.String())
	}
	if gotOp != "get" {
		t.Errorf("op = %q", gotOp)
	}
	if string(gotInput) != "protocol=https\nhost=forgejo.example\n\n" {
		t.Errorf("input = %q", gotInput)
	}
	var resp credentialResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, rr.Body.String())
	}
	if resp.Exit != 0 || resp.Output != "username=me\npassword=secret\n" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestHandler_CredentialPropagatesExit(t *testing.T) {
	h := newCredentialTestHandler(t, func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
		return nil, 1, nil
	})
	rr := doPostTo(t, h, RouteCredential, "tok", credentialBody("get", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d", rr.Code)
	}
	var resp credentialResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Exit != 1 {
		t.Errorf("exit = %d, want 1", resp.Exit)
	}
}

func TestHandler_CredentialRejectUnknownOp(t *testing.T) {
	for _, op := range []string{"fill", "approve", "delete", "up", ""} {
		called := false
		h := newCredentialTestHandler(t, func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
			called = true
			return nil, 0, nil
		})
		rr := doPostTo(t, h, RouteCredential, "tok", credentialBody(op, ""))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("op %q: code = %d, want 400", op, rr.Code)
		}
		if called {
			t.Errorf("op %q: executor must not run", op)
		}
	}
}

func TestHandler_CredentialRejectBadToken(t *testing.T) {
	called := false
	h := newCredentialTestHandler(t, func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	})
	for _, token := range []string{"", "wrong"} {
		rr := doPostTo(t, h, RouteCredential, token, credentialBody("get", ""))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("token=%q code = %d", token, rr.Code)
		}
	}
	if called {
		t.Error("executor must not run on bad token")
	}
}

func TestHandler_CredentialExecErrorIs502(t *testing.T) {
	h := newCredentialTestHandler(t, func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
		return nil, 0, io.EOF
	})
	rr := doPostTo(t, h, RouteCredential, "tok", credentialBody("store", ""))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("code = %d, want 502", rr.Code)
	}
}

func TestHandler_CredentialSeparateRateLimit(t *testing.T) {
	h := newCredentialTestHandler(t, func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
		return nil, 0, nil
	})
	// Exhaust the shared /open bucket (burst 5); the 6th /open is throttled.
	for range 5 {
		if rr := doPost(t, h, "tok", `{"url":"https://example.com"}`); rr.Code != http.StatusNoContent {
			t.Fatalf("open burst code = %d", rr.Code)
		}
	}
	if rr := doPost(t, h, "tok", `{"url":"https://example.com"}`); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("shared bucket not exhausted: code = %d", rr.Code)
	}
	// /credential rides its own bucket, so it is unaffected — a clone must not
	// 429 because URL opens happened first.
	if rr := doPostTo(t, h, RouteCredential, "tok", credentialBody("get", "")); rr.Code != http.StatusOK {
		t.Errorf("credential throttled by shared bucket: code = %d", rr.Code)
	}
}

func TestHandler_CredentialRateLimited(t *testing.T) {
	h := newCredentialTestHandler(t, func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
		return nil, 0, nil
	})
	// Own burst is 15 (fixed test clock → no refill); the 16th is throttled.
	for i := range 15 {
		if rr := doPostTo(t, h, RouteCredential, "tok", credentialBody("get", "")); rr.Code != http.StatusOK {
			t.Fatalf("credential burst[%d] code = %d", i, rr.Code)
		}
	}
	if rr := doPostTo(t, h, RouteCredential, "tok", credentialBody("get", "")); rr.Code != http.StatusTooManyRequests {
		t.Errorf("post-burst credential code = %d, want 429", rr.Code)
	}
}

func TestHandler_HealthZ(t *testing.T) {
	h, _, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, RouteHealth, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("code = %d", rr.Code)
	}
}

// newSoundTestHandler builds a handler whose /sound player is the given fake;
// every other endpoint stays inert.
func newSoundTestHandler(t *testing.T, fn func(data []byte) error) http.Handler {
	t.Helper()
	return buildTestHandler(t, handlerFns{sound: fn})
}

// soundBody is the body the paplay shim POSTs: the MP3 bytes base64-encoded,
// plus the temp-file name herdr chose (audit log only).
func soundBody(name string, data []byte) string {
	b, _ := json.Marshal(map[string]string{"name": name, "data": base64.StdEncoding.EncodeToString(data)})
	return string(b)
}

// The payload carries content, not a path (ADR-0009): the daemon must hand the
// player exactly the bytes the container wrote, and answer 200 without waiting
// for playback.
func TestHandler_SoundPlaysTheDecodedBytes(t *testing.T) {
	var got []byte
	h := newSoundTestHandler(t, func(data []byte) error {
		got = data
		return nil
	})

	rr := doPostTo(t, h, RouteSound, "tok", soundBody("herdr-sound-42-1.mp3", []byte("ID3\x04mp3-bytes")))

	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %q", rr.Code, rr.Body.String())
	}
	if string(got) != "ID3\x04mp3-bytes" {
		t.Errorf("player received %q, want the decoded MP3 bytes", got)
	}
}

// The cap is the ADR's number rather than the 4 KiB the URL routes reuse: a
// developer's own ui.sound.*_path override must fit, and only a payload past
// it is refused.
func TestHandler_SoundBodyCap(t *testing.T) {
	h := newSoundTestHandler(t, func([]byte) error { return nil })

	custom := soundBody("custom.mp3", bytes.Repeat([]byte("x"), 200<<10))
	if rr := doPostTo(t, h, RouteSound, "tok", custom); rr.Code != http.StatusOK {
		t.Errorf("a 200 KiB chime was refused: code = %d", rr.Code)
	}
	over := `{"name":"a.mp3","data":"` + strings.Repeat("A", maxSoundBody) + `"}`
	if rr := doPostTo(t, h, RouteSound, "tok", over); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("code = %d, want 413 past the %d-byte cap", rr.Code, maxSoundBody)
	}
}

// A payload that is not decodable base64, or decodes to nothing, must never
// reach the player: the daemon would otherwise write an empty temp file and
// spawn a process on it.
func TestHandler_SoundRejectsUnusablePayload(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"not base64", `{"name":"a.mp3","data":"not base64!!"}`},
		{"empty", `{"name":"a.mp3","data":""}`},
		{"absent", `{"name":"a.mp3"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			h := newSoundTestHandler(t, func([]byte) error {
				calls.Add(1)
				return nil
			})
			rr := doPostTo(t, h, RouteSound, "tok", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("code = %d, want 400", rr.Code)
			}
			if calls.Load() != 0 {
				t.Error("player was called on an unusable payload")
			}
		})
	}
}

// /sound has its own token bucket: a burst of chimes must not starve an OAuth
// redirect on /open, and a burst of URL opens must not swallow a chime.
func TestHandler_SoundRidesItsOwnBucket(t *testing.T) {
	h := buildTestHandler(t, handlerFns{})
	for i := range rateBurst {
		if rr := doPost(t, h, "tok", `{"url":"https://example.com"}`); rr.Code != http.StatusNoContent {
			t.Fatalf("open burst[%d] code = %d", i, rr.Code)
		}
	}
	if rr := doPost(t, h, "tok", `{"url":"https://example.com"}`); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("shared bucket not exhausted: code = %d", rr.Code)
	}

	// Fixed test clock → no refill, so the sound bucket drains on its own
	// burst and only then throttles.
	for i := range soundRateBurst {
		if rr := doPostTo(t, h, RouteSound, "tok", soundBody("a.mp3", []byte("x"))); rr.Code != http.StatusOK {
			t.Fatalf("sound burst[%d] code = %d — throttled by the shared bucket", i, rr.Code)
		}
	}
	if rr := doPostTo(t, h, RouteSound, "tok", soundBody("a.mp3", []byte("x"))); rr.Code != http.StatusTooManyRequests {
		t.Errorf("post-burst sound code = %d, want 429", rr.Code)
	}
}

// A player that cannot start is the one failure the daemon can still report —
// the shim turns the non-2xx into a non-zero exit, so herdr falls through its
// own chain and logs the aggregate warning.
func TestHandler_SoundReportsAPlayerThatCannotStart(t *testing.T) {
	h := newSoundTestHandler(t, func([]byte) error { return ErrNoSoundPlayer })

	rr := doPostTo(t, h, RouteSound, "tok", soundBody("a.mp3", []byte("x")))

	if rr.Code != http.StatusBadGateway {
		t.Errorf("code = %d, want 502", rr.Code)
	}
}
