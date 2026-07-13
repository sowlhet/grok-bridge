package account_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/wlhet/grok-bridge/internal/account"
)

func TestPickerWeightedPrefersHigherWeight(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	a, err := store.UpsertFromOAuthJSON(ctx,
		[]byte(`{"access_token":"ta","refresh_token":"ra","email":"a@example.com","sub":"sa"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.UpsertFromOAuthJSON(ctx,
		[]byte(`{"access_token":"tb","refresh_token":"rb","email":"b@example.com","sub":"sb"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWeight(ctx, a.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWeight(ctx, b.ID, 1); err != nil {
		t.Fatal(err)
	}

	p := &account.Picker{Store: store, Scheduling: "weighted"}
	counts := map[string]int{}
	for i := 0; i < 40; i++ {
		acc, err := p.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		counts[acc.ID]++
	}
	if counts[a.ID] <= counts[b.ID] {
		t.Fatalf("expected higher weight preferred: %v", counts)
	}
	if counts[a.ID] < 25 {
		t.Fatalf("expected A roughly 3/4 of picks, got %v (total check %s)", counts, fmt.Sprint(counts[a.ID]+counts[b.ID]))
	}
}
