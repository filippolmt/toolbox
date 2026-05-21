package browserbridge

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestHandler(t *testing.T, openErr error) (http.Handler, *atomic.Int32, *strings.Builder) {
	t.Helper()
	var calls atomic.Int32
	openFn := func(_ context.Context, _ string) error {
		calls.Add(1)
		return openErr
	}
	var logBuf strings.Builder
	logger := log.New(&logBuf, "", 0)
	now := func() time.Time { return time.Unix(0, 0) }
	h := newHandler("tok", openFn, logger, now)
	return h, &calls, &logBuf
}

func doPost(t *testing.T, h http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/open", strings.NewReader(body))
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

func TestHandler_HealthZ(t *testing.T) {
	h, _, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("code = %d", rr.Code)
	}
}
