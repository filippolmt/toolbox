package browserbridge

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
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
		fns.proximo = func(_ context.Context, _ string) ([]byte, int, error) { return nil, 0, nil }
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
func newProximoTestHandler(t *testing.T, fn func(ctx context.Context, command string) ([]byte, int, error)) http.Handler {
	t.Helper()
	return buildTestHandler(t, handlerFns{proximo: fn})
}

func doPost(t *testing.T, h http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doPostTo(t, h, "/open", token, body)
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
	req := httptest.NewRequest(http.MethodGet, "/open", nil)
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
	rr := doPostTo(t, h, "/edit", "tok", editBody("code", t.TempDir()))
	if rr.Code != http.StatusNoContent {
		t.Errorf("code = %d, body=%q", rr.Code, rr.Body.String())
	}
	if editCalls.Load() != 1 {
		t.Errorf("editFn calls = %d", editCalls.Load())
	}
}

func TestHandler_EditRejectUnknownEditor(t *testing.T) {
	h, _, editCalls := newTestHandler(t, nil)
	rr := doPostTo(t, h, "/edit", "tok", editBody("vim; rm -rf /", t.TempDir()))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rr.Code)
	}
	if editCalls.Load() != 0 {
		t.Errorf("editFn should not be called on unknown editor")
	}
}

func TestHandler_EditRejectMissingPath(t *testing.T) {
	h, _, editCalls := newTestHandler(t, nil)
	rr := doPostTo(t, h, "/edit", "tok", editBody("code", "/nope/missing"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rr.Code)
	}
	if editCalls.Load() != 0 {
		t.Errorf("editFn should not be called on missing path")
	}
}

func TestHandler_EditRejectRelativePath(t *testing.T) {
	h, _, editCalls := newTestHandler(t, nil)
	rr := doPostTo(t, h, "/edit", "tok", editBody("code", "relative/path"))
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
		rr := doPostTo(t, h, "/edit", token, editBody("code", t.TempDir()))
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
	rr := doPostTo(t, h, "/edit", "tok", editBody("code", t.TempDir()))
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("post-burst /edit code = %d, want 429 (shared limiter)", rr.Code)
	}
}

func proximoBody(command string) string {
	b, _ := json.Marshal(map[string]string{"command": command})
	return string(b)
}

func TestHandler_ProximoOK(t *testing.T) {
	var gotCmd string
	h := newProximoTestHandler(t, func(_ context.Context, command string) ([]byte, int, error) {
		gotCmd = command
		return []byte("stack started\n"), 0, nil
	})
	rr := doPostTo(t, h, "/proximo", "tok", proximoBody("up"))
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
	h := newProximoTestHandler(t, func(_ context.Context, _ string) ([]byte, int, error) {
		return []byte("compose failed\n"), 3, nil
	})
	rr := doPostTo(t, h, "/proximo", "tok", proximoBody("up"))
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

func TestHandler_ProximoRejectUnknownCommand(t *testing.T) {
	for _, cmd := range []string{"install", "uninstall", "config", "up --observability", ""} {
		called := false
		h := newProximoTestHandler(t, func(_ context.Context, _ string) ([]byte, int, error) {
			called = true
			return nil, 0, nil
		})
		rr := doPostTo(t, h, "/proximo", "tok", proximoBody(cmd))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("command %q: code = %d, want 400", cmd, rr.Code)
		}
		if called {
			t.Errorf("command %q: executor must not run", cmd)
		}
	}
}

func TestHandler_ProximoRejectBadToken(t *testing.T) {
	called := false
	h := newProximoTestHandler(t, func(_ context.Context, _ string) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	})
	for _, token := range []string{"", "wrong"} {
		rr := doPostTo(t, h, "/proximo", token, proximoBody("up"))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("token=%q code = %d", token, rr.Code)
		}
	}
	if called {
		t.Error("executor must not run on bad token")
	}
}

func TestHandler_ProximoExecErrorIs502(t *testing.T) {
	h := newProximoTestHandler(t, func(_ context.Context, _ string) ([]byte, int, error) {
		return nil, 0, io.EOF
	})
	rr := doPostTo(t, h, "/proximo", "tok", proximoBody("status"))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("code = %d, want 502", rr.Code)
	}
}

func TestHandler_ProximoBudgetExceedsRequestTimeout(t *testing.T) {
	var deadline time.Time
	h := newProximoTestHandler(t, func(ctx context.Context, _ string) ([]byte, int, error) {
		deadline, _ = ctx.Deadline()
		return nil, 0, nil
	})
	before := time.Now()
	rr := doPostTo(t, h, "/proximo", "tok", proximoBody("up"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d", rr.Code)
	}
	if got := deadline.Sub(before); got < 60*time.Second {
		t.Errorf("execution budget = %v, want >= 60s (first `up` pulls images)", got)
	}
}

func TestHandler_ProximoSharesRateLimit(t *testing.T) {
	h := newProximoTestHandler(t, func(_ context.Context, _ string) ([]byte, int, error) {
		return nil, 0, nil
	})
	for i := range 5 {
		rr := doPostTo(t, h, "/proximo", "tok", proximoBody("status"))
		if rr.Code != http.StatusOK {
			t.Fatalf("burst[%d] code = %d", i, rr.Code)
		}
	}
	rr := doPostTo(t, h, "/proximo", "tok", proximoBody("status"))
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("post-burst code = %d, want 429 (shared limiter)", rr.Code)
	}
}

func TestHandler_HealthZ(t *testing.T) {
	h, _, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("code = %d", rr.Code)
	}
}
