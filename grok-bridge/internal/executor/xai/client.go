// Package xai provides an HTTP executor for xAI Grok Responses API (including SSE).
package xai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wlhet/grok-bridge/internal/account"
	xaiauth "github.com/wlhet/grok-bridge/internal/auth/xai"
)

const (
	tokenAuthHeader = "X-XAI-Token-Auth"
	tokenAuthValue  = "xai-grok-cli"
	// clientVersion is the Grok CLI client version chat-proxy expects.
	// Bump when Grok CLI / chat-proxy requires a newer version.
	clientVersion       = "0.2.93"
	clientVersionHeader = "x-grok-client-version"
)

// Client performs xAI Responses HTTP requests.
type Client struct {
	HTTP *http.Client
}

// Result holds a fully-buffered non-stream response.
// DoResponses returns *http.Response so callers can stream; Result is for buffered helpers.
type Result struct {
	StatusCode int
	Header     http.Header
	Body       []byte // non-stream
}

// ChatBaseURL resolves the base URL for OAuth chat requests.
// Empty or official DefaultAPIBaseURL is rewritten to CLIChatProxyBaseURL;
// any other explicit BaseURL is honored as-is.
func ChatBaseURL(acc account.Account) string {
	base := strings.TrimSpace(acc.BaseURL)
	if base == "" || isDefaultAPIBaseURL(base) {
		return xaiauth.CLIChatProxyBaseURL
	}
	return base
}

func normalizeBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func isDefaultAPIBaseURL(baseURL string) bool {
	return normalizeBaseURL(baseURL) == normalizeBaseURL(xaiauth.DefaultAPIBaseURL)
}

func isCLIChatProxyBaseURL(baseURL string) bool {
	return normalizeBaseURL(baseURL) == normalizeBaseURL(xaiauth.CLIChatProxyBaseURL)
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	// Default: no overall Timeout (streams may run long) but bound response headers.
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
		},
	}
}

// DoResponses POSTs body to {ChatBaseURL}/responses.
// When stream is true, Accept is text/event-stream; otherwise application/json.
// Caller owns the returned *http.Response body and must close it.
func (c *Client) DoResponses(ctx context.Context, acc account.Account, body []byte, stream bool) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base := ChatBaseURL(acc)
	url := strings.TrimSuffix(base, "/") + "/responses"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("xai responses: create request: %w", err)
	}
	applyHeaders(req, acc.AccessToken, stream, isCLIChatProxyBaseURL(base))

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("xai responses request failed: %w", err)
	}
	return resp, nil
}

func applyHeaders(r *http.Request, accessToken string, stream bool, cliChatProxy bool) {
	r.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(accessToken); token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if stream {
		r.Header.Set("Accept", "text/event-stream")
	} else {
		r.Header.Set("Accept", "application/json")
	}
	if cliChatProxy {
		r.Header.Set(tokenAuthHeader, tokenAuthValue)
		r.Header.Set(clientVersionHeader, clientVersion)
	}
}
