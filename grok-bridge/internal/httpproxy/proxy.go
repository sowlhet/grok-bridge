// Package httpproxy builds HTTP clients that honor an optional upstream proxy URL.
package httpproxy

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// NewTransport returns an *http.Transport.
// If proxyURL is non-empty it is used as the fixed proxy; otherwise ProxyFromEnvironment.
func NewTransport(proxyURL string, responseHeaderTimeout time.Duration) (*http.Transport, error) {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = 120 * time.Second
	}
	tr := &http.Transport{
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	proxyURL = trim(proxyURL)
	if proxyURL == "" {
		tr.Proxy = http.ProxyFromEnvironment
		return tr, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse http_proxy %q: %w", proxyURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid http_proxy %q: need scheme and host", proxyURL)
	}
	tr.Proxy = http.ProxyURL(u)
	return tr, nil
}

// NewClient builds a client for long-lived upstream (no overall Timeout).
func NewClient(proxyURL string, responseHeaderTimeout time.Duration) (*http.Client, error) {
	tr, err := NewTransport(proxyURL, responseHeaderTimeout)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: tr}, nil
}

// NewTimeoutClient builds a short-timeout client (OAuth etc.).
func NewTimeoutClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tr, err := NewTransport(proxyURL, timeout)
	if err != nil {
		return nil, err
	}
	// ResponseHeaderTimeout already set; overall Timeout for non-stream OAuth.
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
