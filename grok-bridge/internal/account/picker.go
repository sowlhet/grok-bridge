package account

import (
	"context"
	"fmt"
	"sync"
)

// Picker selects active accounts by strategy.
// Scheduling: "round_robin" (default) or "weighted".
type Picker struct {
	Store      *Store
	Scheduling string

	mu sync.Mutex
	rr uint64
}

// Next returns the next active account according to Scheduling.
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

	mode := p.Scheduling
	if mode == "" {
		mode = "round_robin"
	}

	var a Account
	switch mode {
	case "weighted":
		a = pickWeighted(active, p.rr)
		p.rr++
	default: // round_robin
		idx := p.rr % uint64(len(active))
		p.rr++
		a = active[idx]
	}
	return &a, nil
}

// pickWeighted expands accounts by weight (min 1) and picks round-robin across the expanded list.
func pickWeighted(active []Account, rr uint64) Account {
	type slot struct {
		idx int
	}
	var slots []slot
	for i, a := range active {
		w := a.Weight
		if w <= 0 {
			w = 1
		}
		// Cap single-account expansion to avoid huge slices from bad data.
		if w > 1000 {
			w = 1000
		}
		for j := 0; j < w; j++ {
			slots = append(slots, slot{idx: i})
		}
	}
	if len(slots) == 0 {
		return active[0]
	}
	return active[slots[rr%uint64(len(slots))].idx]
}

// MarkError sets account status to error with the given message.
func (p *Picker) MarkError(ctx context.Context, id, msg string) error {
	return p.Store.SetStatus(ctx, id, "error", msg)
}

// MarkActive sets account status back to active and clears the error message.
func (p *Picker) MarkActive(ctx context.Context, id string) error {
	return p.Store.SetStatus(ctx, id, "active", "")
}

// SetScheduling updates the selection strategy at runtime.
func (p *Picker) SetScheduling(mode string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if mode == "" {
		mode = "round_robin"
	}
	p.Scheduling = mode
}
