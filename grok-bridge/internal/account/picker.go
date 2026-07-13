package account

import (
	"context"
	"fmt"
	"sync"
)

// Picker selects active accounts in round-robin order.
type Picker struct {
	Store *Store
	mu    sync.Mutex
	rr    uint64
}

// Next returns the next active account in round-robin order.
// Returns an error when no active accounts are available.
func (p *Picker) Next(ctx context.Context) (*Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	active, err := p.Store.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("no active accounts")
	}

	idx := p.rr % uint64(len(active))
	p.rr++
	a := active[idx]
	return &a, nil
}

// MarkError sets account status to error with the given message.
func (p *Picker) MarkError(ctx context.Context, id, msg string) error {
	return p.Store.SetStatus(ctx, id, "error", msg)
}

// MarkActive sets account status back to active and clears the error message.
func (p *Picker) MarkActive(ctx context.Context, id string) error {
	return p.Store.SetStatus(ctx, id, "active", "")
}
