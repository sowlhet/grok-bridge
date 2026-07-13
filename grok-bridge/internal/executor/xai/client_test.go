package xai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/wlhet/grok-bridge/internal/account"
	xaiauth "github.com/wlhet/grok-bridge/internal/auth/xai"
)

func TestChatBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "empty rewrites to CLI chat-proxy",
			baseURL: "",
			want:    xaiauth.CLIChatProxyBaseURL,
		},
		{
			name:    "official default rewrites to CLI chat-proxy",
			baseURL: xaiauth.DefaultAPIBaseURL,
			want:    xaiauth.CLIChatProxyBaseURL,
		},
		{
			name:    "official default with trailing slash rewrites",
			baseURL: xaiauth.DefaultAPIBaseURL + "/",
			want:    xaiauth.CLIChatProxyBaseURL,
		},
		{
			name:    "explicit CLI chat-proxy preserved",
			baseURL: xaiauth.CLIChatProxyBaseURL,
			want:    xaiauth.CLIChatProxyBaseURL,
		},
		{
			name:    "custom non-default base honored",
			baseURL: "https://gateway.example.com/v1",
			want:    "https://gateway.example.com/v1",
		},
		{
			name:    "custom base trailing slash trimmed only for comparison; value returned as-is",
			baseURL: "https://gateway.example.com/v1/",
			want:    "https://gateway.example.com/v1/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChatBaseURL(account.Account{BaseURL: tt.baseURL})
			if got != tt.want {
				t.Fatalf("ChatBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDoResponsesPostsPathAuthAndBody(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotAccept string
		gotCT     string
		gotCLI    string
		gotVer    string
		gotBody   []byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotCT = r.Header.Get("Content-Type")
		gotCLI = r.Header.Get("X-XAI-Token-Auth")
		gotVer = r.Header.Get("x-grok-client-version")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed"}`))
	}))
	defer server.Close()

	c := &Client{HTTP: server.Client()}
	body := []byte(`{"model":"grok-4","input":"hi"}`)
	acc := account.Account{
		AccessToken: "tok-abc",
		BaseURL:     server.URL, // non-default → direct to test server, no CLI headers
	}

	resp, err := c.DoResponses(context.Background(), acc, body, false)
	if err != nil {
		t.Fatalf("DoResponses() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/responses" {
		t.Fatalf("path = %q, want /responses", gotPath)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("Authorization = %q, want Bearer tok-abc", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q, want application/json", gotAccept)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", gotCT)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("body = %q, want %q", gotBody, body)
	}
	if gotCLI != "" {
		t.Fatalf("X-XAI-Token-Auth = %q, want empty for custom base", gotCLI)
	}
	if gotVer != "" {
		t.Fatalf("x-grok-client-version = %q, want empty for custom base", gotVer)
	}
}

func TestDoResponsesStreamAccept(t *testing.T) {
	var gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer server.Close()

	c := &Client{HTTP: server.Client()}
	acc := account.Account{
		AccessToken: "stream-tok",
		BaseURL:     server.URL,
	}
	resp, err := c.DoResponses(context.Background(), acc, []byte(`{"model":"grok-4"}`), true)
	if err != nil {
		t.Fatalf("DoResponses() error = %v", err)
	}
	defer resp.Body.Close()

	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept = %q, want text/event-stream", gotAccept)
	}
}

func TestDoResponsesCLIChatProxyHeaders(t *testing.T) {
	var (
		gotAuth string
		gotCLI  string
		gotVer  string
		gotPath string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCLI = r.Header.Get("X-XAI-Token-Auth")
		gotVer = r.Header.Get("x-grok-client-version")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	// Empty/default BaseURL → CLIChatProxyBaseURL; rewrite host to test server.
	c := &Client{HTTP: rewriteHostClient(server.URL)}
	acc := account.Account{
		AccessToken: "oauth-tok",
		BaseURL:     xaiauth.DefaultAPIBaseURL,
	}
	resp, err := c.DoResponses(context.Background(), acc, []byte(`{"model":"grok-4"}`), false)
	if err != nil {
		t.Fatalf("DoResponses() error = %v", err)
	}
	defer resp.Body.Close()

	if gotPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses (CLI base includes /v1)", gotPath)
	}
	if gotAuth != "Bearer oauth-tok" {
		t.Fatalf("Authorization = %q, want Bearer oauth-tok", gotAuth)
	}
	if gotCLI != "xai-grok-cli" {
		t.Fatalf("X-XAI-Token-Auth = %q, want xai-grok-cli", gotCLI)
	}
	if gotVer != clientVersion {
		t.Fatalf("x-grok-client-version = %q, want %q", gotVer, clientVersion)
	}
}

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
