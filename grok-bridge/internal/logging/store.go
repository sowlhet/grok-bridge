// Package logging persists request logs and dashboard aggregates.
package logging

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// LogRecord is one request_logs row.
type LogRecord struct {
	ID              string
	RequestID       string
	CreatedAt       string
	APIKeyID        string
	APIKeyLabel     string
	AccountID       string
	AccountLabel    string
	Protocol        string
	ModelRequested  string
	ModelUpstream   string
	Stream          bool
	StatusCode      int
	ErrorCode       string
	ErrorMessage    string
	LatencyMs       int
	InputTokens     int
	OutputTokens    int
	ClientIP        string
	UserAgent       string
	Path            string
	RequestBody     string
	ResponseBody    string
}

// LogFilter selects request logs for Query.
// Zero values are ignored (except Limit defaults to 100 when unset).
type LogFilter struct {
	Since      string // RFC3339 inclusive lower bound on created_at
	Until      string // RFC3339 exclusive upper bound on created_at
	AccountID  string
	APIKeyID   string
	Model      string // matches model_requested or model_upstream
	Protocol   string
	StatusCode int // 0 = any
	Stream     *bool
	Limit      int
	Offset     int
}

// NamedCount is a label + count pair for dashboard top-N lists.
type NamedCount struct {
	Name  string
	Count int
}

// DashboardStats holds volume and error aggregates for the admin dashboard.
type DashboardStats struct {
	TodayCount     int
	TodayErrors    int
	Last7dCount    int
	Last7dErrors   int
	ActiveAccounts int
	TopModels      []NamedCount
	TopAccounts    []NamedCount
}

// RequestLogStore persists request_logs in SQLite.
type RequestLogStore struct {
	DB *sql.DB
}

const logColumns = `
  id, request_id, created_at,
  api_key_id, api_key_label,
  account_id, account_label,
  protocol, model_requested, model_upstream,
  stream, status_code, error_code, error_message,
  latency_ms, input_tokens, output_tokens,
  client_ip, user_agent, path,
  request_body, response_body`

func scanLog(scanner interface {
	Scan(dest ...any) error
}) (LogRecord, error) {
	var r LogRecord
	var stream int
	err := scanner.Scan(
		&r.ID, &r.RequestID, &r.CreatedAt,
		&r.APIKeyID, &r.APIKeyLabel,
		&r.AccountID, &r.AccountLabel,
		&r.Protocol, &r.ModelRequested, &r.ModelUpstream,
		&stream, &r.StatusCode, &r.ErrorCode, &r.ErrorMessage,
		&r.LatencyMs, &r.InputTokens, &r.OutputTokens,
		&r.ClientIP, &r.UserAgent, &r.Path,
		&r.RequestBody, &r.ResponseBody,
	)
	r.Stream = stream != 0
	return r, err
}

// Insert writes a request log row. Empty ID/CreatedAt are filled automatically.
func (s *RequestLogStore) Insert(ctx context.Context, rec LogRecord) error {
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	if rec.RequestID == "" {
		rec.RequestID = rec.ID
	}
	if rec.CreatedAt == "" {
		rec.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	stream := 0
	if rec.Stream {
		stream = 1
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO request_logs (
  id, request_id, created_at,
  api_key_id, api_key_label,
  account_id, account_label,
  protocol, model_requested, model_upstream,
  stream, status_code, error_code, error_message,
  latency_ms, input_tokens, output_tokens,
  client_ip, user_agent, path,
  request_body, response_body
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, rec.ID, rec.RequestID, rec.CreatedAt,
		rec.APIKeyID, rec.APIKeyLabel,
		rec.AccountID, rec.AccountLabel,
		rec.Protocol, rec.ModelRequested, rec.ModelUpstream,
		stream, rec.StatusCode, rec.ErrorCode, rec.ErrorMessage,
		rec.LatencyMs, rec.InputTokens, rec.OutputTokens,
		rec.ClientIP, rec.UserAgent, rec.Path,
		rec.RequestBody, rec.ResponseBody,
	)
	if err != nil {
		return fmt.Errorf("insert request_log: %w", err)
	}
	return nil
}

// Get returns a request log by id, or (nil, nil) if not found.
func (s *RequestLogStore) Get(ctx context.Context, id string) (*LogRecord, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT`+logColumns+`
FROM request_logs
WHERE id = ?
`, id)
	r, err := scanLog(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get request_log: %w", err)
	}
	return &r, nil
}

// Query returns request logs matching filter, newest first.
func (s *RequestLogStore) Query(ctx context.Context, f LogFilter) ([]LogRecord, error) {
	var (
		conds []string
		args  []any
	)
	if f.Since != "" {
		conds = append(conds, "created_at >= ?")
		args = append(args, f.Since)
	}
	if f.Until != "" {
		conds = append(conds, "created_at < ?")
		args = append(args, f.Until)
	}
	if f.AccountID != "" {
		conds = append(conds, "account_id = ?")
		args = append(args, f.AccountID)
	}
	if f.APIKeyID != "" {
		conds = append(conds, "api_key_id = ?")
		args = append(args, f.APIKeyID)
	}
	if f.Model != "" {
		conds = append(conds, "(model_requested = ? OR model_upstream = ?)")
		args = append(args, f.Model, f.Model)
	}
	if f.Protocol != "" {
		conds = append(conds, "protocol = ?")
		args = append(args, f.Protocol)
	}
	if f.StatusCode != 0 {
		conds = append(conds, "status_code = ?")
		args = append(args, f.StatusCode)
	}
	if f.Stream != nil {
		v := 0
		if *f.Stream {
			v = 1
		}
		conds = append(conds, "stream = ?")
		args = append(args, v)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	q := `SELECT` + logColumns + ` FROM request_logs`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, " AND ")
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query request_logs: %w", err)
	}
	defer rows.Close()

	var out []LogRecord
	for rows.Next() {
		r, err := scanLog(rows)
		if err != nil {
			return nil, fmt.Errorf("scan request_log: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query request_logs rows: %w", err)
	}
	if out == nil {
		out = []LogRecord{}
	}
	return out, nil
}

// Dashboard returns volume/error aggregates and top models/accounts.
func (s *RequestLogStore) Dashboard(ctx context.Context) (DashboardStats, error) {
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	weekStart := now.AddDate(0, 0, -7).Format(time.RFC3339)

	var st DashboardStats

	countRange := func(since string, errorsOnly bool) (int, error) {
		q := `SELECT COUNT(*) FROM request_logs WHERE created_at >= ?`
		if errorsOnly {
			q += ` AND status_code >= 400`
		}
		var n int
		err := s.DB.QueryRowContext(ctx, q, since).Scan(&n)
		return n, err
	}

	var err error
	if st.TodayCount, err = countRange(todayStart, false); err != nil {
		return st, fmt.Errorf("dashboard today count: %w", err)
	}
	if st.TodayErrors, err = countRange(todayStart, true); err != nil {
		return st, fmt.Errorf("dashboard today errors: %w", err)
	}
	if st.Last7dCount, err = countRange(weekStart, false); err != nil {
		return st, fmt.Errorf("dashboard 7d count: %w", err)
	}
	if st.Last7dErrors, err = countRange(weekStart, true); err != nil {
		return st, fmt.Errorf("dashboard 7d errors: %w", err)
	}

	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE status = 'active'`,
	).Scan(&st.ActiveAccounts); err != nil {
		return st, fmt.Errorf("dashboard active accounts: %w", err)
	}

	top := func(col string) ([]NamedCount, error) {
		rows, err := s.DB.QueryContext(ctx, `
SELECT `+col+`, COUNT(*) AS n
FROM request_logs
WHERE created_at >= ? AND `+col+` != ''
GROUP BY `+col+`
ORDER BY n DESC
LIMIT 5
`, weekStart)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []NamedCount
		for rows.Next() {
			var nc NamedCount
			if err := rows.Scan(&nc.Name, &nc.Count); err != nil {
				return nil, err
			}
			out = append(out, nc)
		}
		if out == nil {
			out = []NamedCount{}
		}
		return out, rows.Err()
	}

	if st.TopModels, err = top("model_upstream"); err != nil {
		return st, fmt.Errorf("dashboard top models: %w", err)
	}
	if st.TopAccounts, err = top("account_label"); err != nil {
		return st, fmt.Errorf("dashboard top accounts: %w", err)
	}
	return st, nil
}

// DeleteOlderThan removes request_logs rows with created_at strictly before cutoff.
// Returns the number of deleted rows.
func (s *RequestLogStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("log store not configured")
	}
	if cutoff.IsZero() {
		return 0, fmt.Errorf("cutoff is zero")
	}
	// created_at is stored as RFC3339 UTC strings; compare lexicographically.
	cutoffStr := cutoff.UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx, `DELETE FROM request_logs WHERE created_at < ?`, cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("delete old request_logs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete old request_logs rows: %w", err)
	}
	return n, nil
}

// DeleteOlderThanDays deletes logs older than retentionDays.
// retentionDays <= 0 means no purge (returns 0, nil).
func (s *RequestLogStore) DeleteOlderThanDays(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	return s.DeleteOlderThan(ctx, cutoff)
}
