package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wlhet/grok-bridge/internal/api"
)

func TestHealthz(t *testing.T) {
	s := api.NewServer(api.ServerDeps{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("body=%q", rr.Body.String())
	}
}
