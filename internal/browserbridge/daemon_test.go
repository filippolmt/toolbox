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

func newTestHandler(t *testing.T, openErr error) (http.Handler, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var openCalls, editCalls atomic.Int32
	openFn := func(_ context.Context, _ string) error {
		openCalls.Add(1)
		return openErr
	}
	editFn := func(_ context.Context, _, _ string) error {
		editCalls.Add(1)
		return nil
	}
	var logBuf strings.Builder
	logger := log.New(&logBuf, "", 0)
	now := func() time.Time { return time.Unix(0, 0) }
	h := newHandler("tok", openFn, editFn, logger, now)
	return h, &openCalls, &editCalls
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

func TestHandler_HealthZ(t *testing.T) {
	h, _, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("code = %d", rr.Code)
	}
}
