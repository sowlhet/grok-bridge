// Package runtime holds small process-wide helpers (concurrency gates, etc.).
package runtime

import (
	"context"
	"fmt"
	"sync"
)

// Limiter gates global and per-account concurrent upstream requests.
// Zero limits mean unlimited.
type Limiter struct {
	mu sync.Mutex

	globalLimit  int
	accountLimit int

	globalSem chan struct{}
	// per-account semaphores (lazy)
	accountSem map[string]chan struct{}
}

// NewLimiter builds a limiter. maxGlobal/maxPerAccount <= 0 means unlimited.
func NewLimiter(maxGlobal, maxPerAccount int) *Limiter {
	l := &Limiter{
		globalLimit:  maxGlobal,
		accountLimit: maxPerAccount,
		accountSem:   make(map[string]chan struct{}),
	}
	if maxGlobal > 0 {
		l.globalSem = make(chan struct{}, maxGlobal)
	}
	return l
}

// Configure updates limits at runtime. In-flight acquisitions are unaffected;
// new Acquire calls use the new limits. Existing per-account channels are reset
// only when the per-account limit value changes.
func (l *Limiter) Configure(maxGlobal, maxPerAccount int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if maxGlobal != l.globalLimit {
		l.globalLimit = maxGlobal
		if maxGlobal > 0 {
			l.globalSem = make(chan struct{}, maxGlobal)
		} else {
			l.globalSem = nil
		}
	}
	if maxPerAccount != l.accountLimit {
		l.accountLimit = maxPerAccount
		l.accountSem = make(map[string]chan struct{})
	}
}

// Snapshot returns current limits.
func (l *Limiter) Snapshot() (maxGlobal, maxPerAccount int) {
	if l == nil {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.globalLimit, l.accountLimit
}

// Acquire blocks until a global slot and an account slot are available, or ctx is done.
// The returned release function must be called exactly once.
func (l *Limiter) Acquire(ctx context.Context, accountID string) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Global first.
	if err := l.acquireGlobal(ctx); err != nil {
		return nil, err
	}
	// Then per-account.
	if err := l.acquireAccount(ctx, accountID); err != nil {
		l.releaseGlobal()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.releaseAccount(accountID)
			l.releaseGlobal()
		})
	}, nil
}

func (l *Limiter) acquireGlobal(ctx context.Context) error {
	l.mu.Lock()
	sem := l.globalSem
	l.mu.Unlock()
	if sem == nil {
		return nil
	}
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait global concurrency: %w", ctx.Err())
	}
}

func (l *Limiter) releaseGlobal() {
	l.mu.Lock()
	sem := l.globalSem
	l.mu.Unlock()
	if sem == nil {
		return
	}
	select {
	case <-sem:
	default:
	}
}

func (l *Limiter) accountChannel(accountID string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.accountLimit <= 0 {
		return nil
	}
	if accountID == "" {
		accountID = "_"
	}
	ch, ok := l.accountSem[accountID]
	if !ok {
		ch = make(chan struct{}, l.accountLimit)
		l.accountSem[accountID] = ch
	}
	return ch
}

func (l *Limiter) acquireAccount(ctx context.Context, accountID string) error {
	sem := l.accountChannel(accountID)
	if sem == nil {
		return nil
	}
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait account concurrency: %w", ctx.Err())
	}
}

func (l *Limiter) releaseAccount(accountID string) {
	sem := l.accountChannel(accountID)
	if sem == nil {
		return
	}
	select {
	case <-sem:
	default:
	}
}
