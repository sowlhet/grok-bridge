// Package pipeline executes the full request path: pick account, translate,
// call xAI, retry/switch on errors, translate the response, and log.
package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wlhet/grok-bridge/internal/access"
	"github.com/wlhet/grok-bridge/internal/account"
	xaiauth "github.com/wlhet/grok-bridge/internal/auth/xai"
	"github.com/wlhet/grok-bridge/internal/config"
	xai "github.com/wlhet/grok-bridge/internal/executor/xai"
	"github.com/wlhet/grok-bridge/internal/logging"
	"github.com/wlhet/grok-bridge/internal/models"
	rt "github.com/wlhet/grok-bridge/internal/runtime"
	"github.com/wlhet/grok-bridge/internal/translate"
)

const tokenRefreshSkew = 5 * time.Minute

// Pipeline orchestrates account selection, translation, upstream execution,
// retries, response translation, and request logging.
type Pipeline struct {
	Accounts     *account.Picker
	AccountStore *account.Store
	XAI          *xai.Client
	OAuth        *xaiauth.Client
	Catalog      *models.Catalog
	Logs         *logging.RequestLogStore
	Retry        config.RetryConfig
	LogBodies    string
	// Limiter is optional global/per-account concurrency gate.
	Limiter *rt.Limiter
}

// Inbound is a normalized client request ready for pipeline handling.
type Inbound struct {
	Protocol  translate.Format
	Model     string
	Body      []byte
	Stream    bool
	APIKey    *access.KeyRecord
	Path      string
	ClientIP  string
	UserAgent string
}

// Handle runs the full pipeline and writes the client response to w.
// A request log row is always written (bodies subject to LogBodies policy).
func (p *Pipeline) Handle(ctx context.Context, in Inbound, w http.ResponseWriter) error {
	start := time.Now()
	requestID := uuid.NewString()

	rec := logging.LogRecord{
		RequestID:      requestID,
		Protocol:       string(in.Protocol),
		ModelRequested: in.Model,
		Stream:         in.Stream,
		Path:           in.Path,
		ClientIP:       in.ClientIP,
		UserAgent:      in.UserAgent,
	}
	if in.APIKey != nil {
		rec.APIKeyID = in.APIKey.ID
		rec.APIKeyLabel = in.APIKey.Label
	}

	// Always persist the log on exit with best-effort status.
	var (
		finalStatus    int
		finalErrCode   string
		finalErrMsg    string
		finalResp      []byte
		inputTokens    int
		outputTokens   int
		accountID      string
		accountLabel   string
		upstream       string
		firstTokenAt   time.Time
		markFirstToken = func() {
			if firstTokenAt.IsZero() {
				firstTokenAt = time.Now()
			}
		}
	)
	defer func() {
		rec.StatusCode = finalStatus
		rec.ErrorCode = finalErrCode
		rec.ErrorMessage = finalErrMsg
		total := time.Since(start)
		rec.LatencyMs = int(total.Milliseconds())
		rec.TotalSeconds = roundSeconds(total)
		if !firstTokenAt.IsZero() {
			rec.FirstTokenSeconds = roundSeconds(firstTokenAt.Sub(start))
		} else if finalStatus > 0 {
			// No downstream body bytes observed; fall back to total duration.
			rec.FirstTokenSeconds = rec.TotalSeconds
		}
		rec.InputTokens = inputTokens
		rec.OutputTokens = outputTokens
		rec.AccountID = accountID
		rec.AccountLabel = accountLabel
		rec.ModelUpstream = upstream
		if shouldLogBodies(p.LogBodies, finalStatus) {
			rec.RequestBody = ScrubSecrets(string(in.Body))
			// Cap response body log size to avoid huge SSE dumps.
			respBody := string(finalResp)
			if len(finalResp) > 64*1024 {
				respBody = string(finalResp[:64*1024]) + "…(truncated)"
			}
			rec.ResponseBody = ScrubSecrets(respBody)
		}
		if p.Logs != nil {
			_ = p.Logs.Insert(ctx, rec)
		}
	}()

	// Resolve model.
	if p.Catalog != nil {
		u, err := p.Catalog.Resolve(in.Model)
		if err != nil {
			finalStatus = http.StatusBadRequest
			finalErrCode = "unknown_model"
			finalErrMsg = err.Error()
			writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
			return err
		}
		upstream = u
	} else {
		upstream = in.Model
	}
	rec.ModelUpstream = upstream

	// Translate inbound → xAI Responses body.
	xaiBody, err := translateInbound(in.Protocol, in.Body, upstream)
	if err != nil {
		finalStatus = http.StatusBadRequest
		finalErrCode = "translate_error"
		finalErrMsg = err.Error()
		writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
		return err
	}
	xaiBody, err = setStreamFlag(xaiBody, in.Stream)
	if err != nil {
		finalStatus = http.StatusBadRequest
		finalErrCode = "translate_error"
		finalErrMsg = err.Error()
		writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
		return err
	}

	maxSwitches := p.Retry.MaxAccountSwitches
	if maxSwitches < 0 {
		maxSwitches = 0
	}
	switches := 0
	tried := map[string]struct{}{}

	for {
		acc, err := p.pickAccount(ctx, tried)
		if err != nil {
			if finalStatus == 0 {
				finalStatus = http.StatusServiceUnavailable
				finalErrCode = "no_account"
				finalErrMsg = err.Error()
			}
			if finalStatus < 400 {
				finalStatus = http.StatusServiceUnavailable
			}
			writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
			return err
		}
		accountID = acc.ID
		accountLabel = acc.Label
		if accountLabel == "" {
			accountLabel = acc.Email
		}

		// Concurrency gate (global + per-account). Held for the full upstream call.
		release, aerr := p.acquire(ctx, acc.ID)
		if aerr != nil {
			finalStatus = http.StatusServiceUnavailable
			finalErrCode = "concurrency_limit"
			finalErrMsg = aerr.Error()
			writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
			return aerr
		}

		// Proactive refresh when near expiry.
		if p.nearExpiry(*acc) {
			if refreshed, rerr := p.refreshAccount(ctx, acc); rerr == nil {
				acc = refreshed
			}
			// On proactive refresh failure, still try the current token.
		}

		resp, err := p.XAI.DoResponses(ctx, *acc, xaiBody, in.Stream)
		if err != nil {
			release()
			// Network/transport error: treat as switchable transient.
			finalErrCode = "upstream_error"
			finalErrMsg = err.Error()
			finalStatus = http.StatusBadGateway
			tried[acc.ID] = struct{}{}
			if switches >= maxSwitches {
				writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
				return err
			}
			switches++
			continue
		}

		status := resp.StatusCode

		// 401: refresh once and retry same account.
		if status == http.StatusUnauthorized {
			bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			refreshed, rerr := p.refreshAccount(ctx, acc)
			if rerr != nil {
				release()
				_ = p.Accounts.MarkError(ctx, acc.ID, "401 refresh failed: "+rerr.Error())
				finalStatus = http.StatusUnauthorized
				finalErrCode = "auth_error"
				finalErrMsg = "token refresh failed: " + rerr.Error()
				finalResp = bodySnippet
				tried[acc.ID] = struct{}{}
				if switches >= maxSwitches {
					writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
					return rerr
				}
				switches++
				continue
			}
			// Retry once with refreshed token.
			resp2, err2 := p.XAI.DoResponses(ctx, *refreshed, xaiBody, in.Stream)
			if err2 != nil {
				release()
				_ = p.Accounts.MarkError(ctx, acc.ID, "401 retry failed: "+err2.Error())
				finalStatus = http.StatusBadGateway
				finalErrCode = "upstream_error"
				finalErrMsg = err2.Error()
				tried[acc.ID] = struct{}{}
				if switches >= maxSwitches {
					writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
					return err2
				}
				switches++
				continue
			}
			if resp2.StatusCode == http.StatusUnauthorized {
				bodySnippet2, _ := io.ReadAll(io.LimitReader(resp2.Body, 4096))
				_ = resp2.Body.Close()
				release()
				_ = p.Accounts.MarkError(ctx, acc.ID, "401 after refresh")
				finalStatus = http.StatusUnauthorized
				finalErrCode = "auth_error"
				finalErrMsg = "unauthorized after refresh"
				finalResp = bodySnippet2
				tried[acc.ID] = struct{}{}
				if switches >= maxSwitches {
					writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
					return fmt.Errorf("unauthorized after refresh")
				}
				switches++
				continue
			}
			// Use the retried response for the rest of the flow.
			resp = resp2
			status = resp.StatusCode
			acc = refreshed
			accountID = acc.ID
			accountLabel = acc.Label
			if accountLabel == "" {
				accountLabel = acc.Email
			}
		}

		// 429 / 5xx: switch account.
		if status == http.StatusTooManyRequests || status >= 500 {
			bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			release()
			finalStatus = status
			finalErrCode = "upstream_error"
			finalErrMsg = fmt.Sprintf("upstream status %d", status)
			finalResp = bodySnippet
			tried[acc.ID] = struct{}{}
			if switches >= maxSwitches {
				writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
				return fmt.Errorf("upstream status %d after switches", status)
			}
			switches++
			continue
		}

		// Non-retryable client error from upstream (4xx other than 401/429).
		if status >= 400 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			release()
			finalStatus = status
			finalErrCode = "upstream_error"
			finalErrMsg = fmt.Sprintf("upstream status %d", status)
			finalResp = bodyBytes
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if n, _ := w.Write(bodyBytes); n > 0 {
				markFirstToken()
			}
			return fmt.Errorf("upstream status %d", status)
		}

		// Success path: translate and write.
		if in.Stream {
			finalStatus, finalResp, inputTokens, outputTokens, err = p.writeStream(w, in.Protocol, resp, markFirstToken)
			_ = resp.Body.Close()
			release()
			if err != nil {
				if finalStatus == 0 {
					finalStatus = http.StatusBadGateway
				}
				finalErrCode = "stream_error"
				finalErrMsg = err.Error()
				return err
			}
			return nil
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		release()
		if err != nil {
			finalStatus = http.StatusBadGateway
			finalErrCode = "upstream_error"
			finalErrMsg = err.Error()
			writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
			return err
		}
		finalResp = bodyBytes
		inputTokens, outputTokens = extractUsage(bodyBytes)

		out, err := translateOutbound(in.Protocol, bodyBytes, false)
		if err != nil {
			finalStatus = http.StatusBadGateway
			finalErrCode = "translate_error"
			finalErrMsg = err.Error()
			writeJSONError(w, finalStatus, finalErrCode, finalErrMsg)
			return err
		}
		finalStatus = http.StatusOK
		finalResp = out
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if n, _ := w.Write(out); n > 0 {
			markFirstToken()
		}
		return nil
	}
}

func (p *Pipeline) pickAccount(ctx context.Context, skip map[string]struct{}) (*account.Account, error) {
	if p.Accounts == nil {
		return nil, fmt.Errorf("no account picker")
	}
	// Try a few picks to avoid recently-failed accounts when alternatives exist.
	var first *account.Account
	for i := 0; i < 8; i++ {
		acc, err := p.Accounts.Next(ctx)
		if err != nil {
			if first != nil {
				return first, nil
			}
			return nil, err
		}
		if _, bad := skip[acc.ID]; !bad {
			return acc, nil
		}
		if first == nil {
			first = acc
		}
	}
	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("no active accounts")
}

func (p *Pipeline) nearExpiry(acc account.Account) bool {
	if strings.TrimSpace(acc.ExpiresAt) == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, acc.ExpiresAt)
	if err != nil {
		// Try common variants.
		exp, err = time.Parse(time.RFC3339Nano, acc.ExpiresAt)
		if err != nil {
			return false
		}
	}
	return time.Until(exp) <= tokenRefreshSkew
}

func (p *Pipeline) refreshAccount(ctx context.Context, acc *account.Account) (*account.Account, error) {
	if p.OAuth == nil {
		return nil, fmt.Errorf("oauth client not configured")
	}
	if strings.TrimSpace(acc.RefreshToken) == "" {
		return nil, fmt.Errorf("account %q has no refresh token", acc.ID)
	}
	td, err := p.OAuth.Refresh(ctx, acc.TokenEndpoint, acc.RefreshToken)
	if err != nil {
		return nil, err
	}
	access := td.AccessToken
	refresh := td.RefreshToken
	if refresh == "" {
		refresh = acc.RefreshToken
	}
	idToken := td.IDToken
	if idToken == "" {
		idToken = acc.IDToken
	}
	expiresAt := td.Expire
	lastRefresh := time.Now().UTC().Format(time.RFC3339)
	if p.AccountStore != nil {
		if err := p.AccountStore.UpdateTokens(ctx, acc.ID, access, refresh, idToken, expiresAt, lastRefresh); err != nil {
			return nil, err
		}
		// Reload for consistency.
		if updated, gerr := p.AccountStore.Get(ctx, acc.ID); gerr == nil && updated != nil {
			return updated, nil
		}
	}
	// Mutate local copy if store unavailable.
	cp := *acc
	cp.AccessToken = access
	cp.RefreshToken = refresh
	cp.IDToken = idToken
	cp.ExpiresAt = expiresAt
	cp.LastRefreshAt = lastRefresh
	return &cp, nil
}

func translateInbound(protocol translate.Format, body []byte, model string) ([]byte, error) {
	switch protocol {
	case translate.FormatClaude:
		return translate.ClaudeMessagesToXAI(body, model)
	case translate.FormatOpenAIChat:
		return translate.ChatCompletionsToXAI(body, model)
	case translate.FormatOpenAIResponses, translate.FormatXAI:
		return translate.ResponsesToXAI(body, model)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func translateOutbound(protocol translate.Format, body []byte, stream bool) ([]byte, error) {
	switch protocol {
	case translate.FormatClaude:
		if stream {
			// Stream uses ClaudeSSETranslator; non-event full body path:
			return translate.XAIResponseToClaudeMessage(body)
		}
		return translate.XAIResponseToClaudeMessage(body)
	case translate.FormatOpenAIChat:
		return translate.XAIResponseToChatCompletions(body, stream)
	case translate.FormatOpenAIResponses, translate.FormatXAI:
		if stream {
			return translate.XAIEventToResponsesSSE(body)
		}
		return translate.XAIResponseToResponses(body)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func setStreamFlag(body []byte, stream bool) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	m["stream"] = stream
	return json.Marshal(m)
}

func (p *Pipeline) writeStream(w http.ResponseWriter, protocol translate.Format, resp *http.Response, markFirstToken func()) (status int, logged []byte, inTok, outTok int, err error) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}

	var logBuf bytes.Buffer
	scanner := bufio.NewScanner(resp.Body)
	// Allow large SSE frames.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		claudeT   *translate.ClaudeSSETranslator
		eventType string
		dataLines []string
	)
	if protocol == translate.FormatClaude {
		claudeT = translate.NewClaudeSSETranslator()
	}

	flushEvent := func() error {
		if len(dataLines) == 0 && eventType == "" {
			return nil
		}
		data := []byte(strings.Join(dataLines, "\n"))
		dataLines = nil
		et := eventType
		eventType = ""

		var frames [][]byte
		switch protocol {
		case translate.FormatClaude:
			frames, err = claudeT.Event(et, data)
			if err != nil {
				return err
			}
		case translate.FormatOpenAIChat:
			// Prefer full SSE line form when possible.
			payload := data
			if et != "" {
				// Inject type if missing.
				var root map[string]any
				if json.Unmarshal(payload, &root) == nil {
					if asString(root["type"]) == "" && et != "" {
						root["type"] = et
						if b, mErr := json.Marshal(root); mErr == nil {
							payload = b
						}
					}
				}
			}
			frame, cErr := translate.XAIResponseToChatCompletions(payload, true)
			if cErr != nil {
				return cErr
			}
			if len(frame) > 0 {
				frames = [][]byte{frame}
			}
		case translate.FormatOpenAIResponses, translate.FormatXAI:
			// Rebuild a data line for the helper.
			line := append([]byte("data: "), data...)
			frame, rErr := translate.XAIEventToResponsesSSE(line)
			if rErr != nil {
				return rErr
			}
			if len(frame) > 0 {
				frames = [][]byte{frame}
			}
			if et != "" {
				// Also emit event: line for Responses clients that care.
				evLine := []byte("event: " + et + "\n")
				n, wErr := w.Write(evLine)
				if wErr != nil {
					return wErr
				}
				if n > 0 && markFirstToken != nil {
					markFirstToken()
				}
				logBuf.Write(evLine)
			}
		default:
			return fmt.Errorf("unsupported protocol %q", protocol)
		}

		for _, f := range frames {
			if len(f) == 0 {
				continue
			}
			n, wErr := w.Write(f)
			if wErr != nil {
				return wErr
			}
			if n > 0 && markFirstToken != nil {
				markFirstToken()
			}
			logBuf.Write(f)
			if flusher != nil {
				flusher.Flush()
			}
			// Capture usage from completed events when present.
			if in, out, ok := usageFromEventData(data); ok {
				inTok, outTok = in, out
			}
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flushEvent(); err != nil {
				return http.StatusOK, logBuf.Bytes(), inTok, outTok, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		// Keepalive comments — ignore.
		if strings.HasPrefix(line, ":") {
			continue
		}
	}
	if err := flushEvent(); err != nil {
		return http.StatusOK, logBuf.Bytes(), inTok, outTok, err
	}
	if err := scanner.Err(); err != nil {
		return http.StatusOK, logBuf.Bytes(), inTok, outTok, err
	}
	return http.StatusOK, logBuf.Bytes(), inTok, outTok, nil
}

func extractUsage(body []byte) (in, out int) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return 0, 0
	}
	// Unwrap response.completed envelope.
	if resp, ok := root["response"].(map[string]any); ok {
		if u, ok := resp["usage"].(map[string]any); ok {
			return intFrom(u["input_tokens"]), intFrom(u["output_tokens"])
		}
	}
	if u, ok := root["usage"].(map[string]any); ok {
		return intFrom(u["input_tokens"]), intFrom(u["output_tokens"])
	}
	return 0, 0
}

func usageFromEventData(data []byte) (in, out int, ok bool) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return 0, 0, false
	}
	typ, _ := root["type"].(string)
	if typ != "response.completed" && typ != "response.incomplete" {
		// Bare response object.
		if root["usage"] == nil && root["response"] == nil {
			return 0, 0, false
		}
	}
	in, out = extractUsage(data)
	return in, out, in != 0 || out != 0 || typ == "response.completed"
}

func intFrom(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func shouldLogBodies(policy string, statusCode int) bool {
	switch policy {
	case "all":
		return true
	case "errors_only":
		return statusCode >= 400 || statusCode == 0
	case "sample":
		if statusCode >= 400 || statusCode == 0 {
			return true
		}
		// ~10% of successful requests.
		return time.Now().UnixNano()%10 == 0
	default: // "off" and unknown
		return false
	}
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": msg,
			"type":    code,
		},
	})
}

func (p *Pipeline) acquire(ctx context.Context, accountID string) (func(), error) {
	if p.Limiter == nil {
		return func() {}, nil
	}
	return p.Limiter.Acquire(ctx, accountID)
}

func roundSeconds(d time.Duration) float64 {
	if d < 0 {
		d = 0
	}
	return float64(int(d.Seconds()*100+0.5)) / 100
}
