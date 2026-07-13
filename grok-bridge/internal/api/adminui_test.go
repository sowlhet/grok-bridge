package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wlhet/grok-bridge/internal/api"
)

func TestAdminIndexEmbedded(t *testing.T) {
	s := api.NewServer(api.ServerDeps{
		AdminPassword: "test",
	})
	h := s.Handler()

	for _, path := range []string{"/admin/", "/admin", "/admin/index.html"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
		ct := rr.Header().Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			t.Fatalf("%s Content-Type=%q want text/html", path, ct)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "Grok Bridge") {
			t.Fatalf("%s body missing %q: %s", path, "Grok Bridge", body)
		}
	}
}

func TestAdminStaticAssets(t *testing.T) {
	s := api.NewServer(api.ServerDeps{AdminPassword: "test"})
	h := s.Handler()

	for _, path := range []string{"/admin/static/app.js", "/admin/static/styles.css"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
		if rr.Body.Len() == 0 {
			t.Fatalf("%s empty body", path)
		}
	}
}
