package xai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestValidateOAuthEndpoint(t *testing.T) {
	if _, err := ValidateOAuthEndpoint("https://auth.x.ai/oauth2/token", "token_endpoint"); err != nil {
		t.Fatalf("ValidateOAuthEndpoint(xai) error = %v", err)
	}
	if _, err := ValidateOAuthEndpoint("https://accounts.x.ai/oauth2/device", "device_authorization_endpoint"); err != nil {
		t.Fatalf("ValidateOAuthEndpoint(subdomain) error = %v", err)
	}
	if _, err := ValidateOAuthEndpoint("http://auth.x.ai/oauth2/token", "token_endpoint"); err == nil {
		t.Fatal("expected non-HTTPS endpoint to be rejected")
	}
	if _, err := ValidateOAuthEndpoint("https://evil.example/oauth/token", "token_endpoint"); err == nil {
		t.Fatal("expected non-xAI endpoint to be rejected")
	}
	if _, err := ValidateOAuthEndpoint("", "token_endpoint"); err == nil {
		t.Fatal("expected empty endpoint to be rejected")
	}
}

func TestDiscoverParsesEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	// Discover hardcodes DiscoveryURL; rewriteHostClient maps auth.x.ai → this test server.
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_authorization_endpoint": "https://auth.x.ai/oauth2/device",
			"token_endpoint":                "https://auth.x.ai/oauth2/token",
		})
	})

	c := &Client{HTTP: rewriteHostClient(server.URL)}
	// Discover uses DiscoveryURL; rewriteHostClient maps auth.x.ai → test server.
	d, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if d.DeviceAuthorizationEndpoint != "https://auth.x.ai/oauth2/device" {
		t.Fatalf("device endpoint = %q", d.DeviceAuthorizationEndpoint)
	}
	if d.TokenEndpoint != "https://auth.x.ai/oauth2/token" {
		t.Fatalf("token endpoint = %q", d.TokenEndpoint)
	}
}

func TestDiscoverRejectsNonXAIEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_authorization_endpoint": "https://evil.example/device",
			"token_endpoint":                "https://auth.x.ai/oauth2/token",
		})
	}))
	defer server.Close()

	c := &Client{HTTP: rewriteHostClient(server.URL)}
	if _, err := c.Discover(context.Background()); err == nil {
		t.Fatal("expected Discover to reject non-xAI device endpoint")
	}
}

func TestStartDeviceFlowPostsClientIDAndScope(t *testing.T) {
	var gotForm url.Values
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_authorization_endpoint": "https://auth.x.ai/oauth2/device",
			"token_endpoint":                "https://auth.x.ai/oauth2/token",
		})
	})
	mux.HandleFunc("/oauth2/device", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Fatalf("Content-Type = %q, want form", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "device-abc",
			"user_code":                 "ABCD-1234",
			"verification_uri":          "https://accounts.x.ai/oauth2/device",
			"verification_uri_complete": "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234",
			"expires_in":                1800,
			"interval":                  5,
		})
	})

	c := &Client{HTTP: rewriteHostClient(server.URL)}
	dc, err := c.StartDeviceFlow(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceFlow() error = %v", err)
	}
	if dc.DeviceCode != "device-abc" {
		t.Fatalf("device_code = %q", dc.DeviceCode)
	}
	if dc.UserCode != "ABCD-1234" {
		t.Fatalf("user_code = %q", dc.UserCode)
	}
	if dc.TokenEndpoint != "https://auth.x.ai/oauth2/token" {
		t.Fatalf("TokenEndpoint = %q", dc.TokenEndpoint)
	}
	if gotForm.Get("client_id") != ClientID {
		t.Fatalf("client_id = %q, want %q", gotForm.Get("client_id"), ClientID)
	}
	if gotForm.Get("scope") != Scope {
		t.Fatalf("scope = %q, want %q", gotForm.Get("scope"), Scope)
	}
}

func TestPollTokenHandlesPendingThenSuccess(t *testing.T) {
	var pollCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != DeviceCodeGrantType {
			t.Fatalf("grant_type = %q, want %q", got, DeviceCodeGrantType)
		}
		if got := r.PostForm.Get("device_code"); got != "device-abc" {
			t.Fatalf("device_code = %q, want device-abc", got)
		}
		if got := r.PostForm.Get("client_id"); got != ClientID {
			t.Fatalf("client_id = %q, want %q", got, ClientID)
		}

		count := atomic.AddInt32(&pollCount, 1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":             "authorization_pending",
				"error_description": "User has not yet authorized",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"id_token":      fakeJWTWithEmail("user@x.ai", "sub-1"),
		})
	}))
	defer server.Close()

	c := &Client{HTTP: rewriteHostClient(server.URL)}
	tokenData, err := c.PollToken(context.Background(), &DeviceCodeResponse{
		DeviceCode:    "device-abc",
		UserCode:      "ABCD-1234",
		ExpiresIn:     60,
		Interval:      1,
		// Must be a validated x.ai host; rewriteHostClient maps auth.x.ai → test server.
		TokenEndpoint: "https://auth.x.ai/oauth2/token",
	})
	if err != nil {
		t.Fatalf("PollToken() error = %v", err)
	}
	if tokenData.AccessToken != "access-1" {
		t.Fatalf("access token = %q, want access-1", tokenData.AccessToken)
	}
	if tokenData.RefreshToken != "refresh-1" {
		t.Fatalf("refresh token = %q, want refresh-1", tokenData.RefreshToken)
	}
	if tokenData.Email != "user@x.ai" {
		t.Fatalf("email = %q, want user@x.ai", tokenData.Email)
	}
	if tokenData.Subject != "sub-1" {
		t.Fatalf("subject = %q, want sub-1", tokenData.Subject)
	}
	if tokenData.Expire == "" {
		t.Fatal("Expire empty, want RFC3339 timestamp")
	}
	if got := atomic.LoadInt32(&pollCount); got != 2 {
		t.Fatalf("poll count = %d, want 2", got)
	}
}

func TestRefreshPostsGrantTypeAndClientID(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Fatalf("Content-Type = %q, want form", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"id_token":      fakeJWTWithEmail("user@x.ai", "sub-1"),
		})
	}))
	defer server.Close()

	c := &Client{HTTP: rewriteHostClient(server.URL)}
	tokenData, err := c.Refresh(context.Background(), "https://auth.x.ai/oauth2/token", "old-refresh")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if tokenData.AccessToken != "new-access" {
		t.Fatalf("access token = %q, want new-access", tokenData.AccessToken)
	}
	if tokenData.RefreshToken != "new-refresh" {
		t.Fatalf("refresh token = %q, want new-refresh", tokenData.RefreshToken)
	}
	if gotForm.Get("grant_type") != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", gotForm.Get("grant_type"))
	}
	if gotForm.Get("client_id") != ClientID {
		t.Fatalf("client_id = %q, want %q", gotForm.Get("client_id"), ClientID)
	}
	if gotForm.Get("refresh_token") != "old-refresh" {
		t.Fatalf("refresh_token = %q, want old-refresh", gotForm.Get("refresh_token"))
	}
}

func TestRefreshRejectsIllegalEndpoint(t *testing.T) {
	c := &Client{HTTP: http.DefaultClient}
	if _, err := c.Refresh(context.Background(), "https://evil.example/token", "rt"); err == nil {
		t.Fatal("expected Refresh to reject non-xAI token_endpoint")
	}
	if _, err := c.Refresh(context.Background(), "http://auth.x.ai/oauth2/token", "rt"); err == nil {
		t.Fatal("expected Refresh to reject non-HTTPS token_endpoint")
	}
}

func TestPollTokenOnceRejectsIllegalEndpoint(t *testing.T) {
	c := &Client{HTTP: http.DefaultClient}
	if _, _, err := c.PollTokenOnce(context.Background(), "device", "https://169.254.169.254/latest"); err == nil {
		t.Fatal("expected PollTokenOnce to reject SSRF-like endpoint")
	}
}

// rewriteHostClient returns an HTTP client that rewrites requests for auth.x.ai
// to the given baseURL (test server), preserving path and query.
func rewriteHostClient(baseURL string) *http.Client {
	target, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.URL.Scheme = target.Scheme
			clone.URL.Host = target.Host
			clone.Host = target.Host
			return http.DefaultTransport.RoundTrip(clone)
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeJWTWithEmail(email, subject string) string {
	header := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(`{"email":"` + email + `","sub":"` + subject + `"}`))
	return header + "." + payload + ".sig"
}
