package account_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/wlhet/grok-bridge/internal/account"
)

func TestPickerRoundRobin(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	var activeIDs []string
	for i, email := range []string{"a@x.ai", "b@x.ai", "c@x.ai"} {
		raw := []byte(fmt.Sprintf(
			`{"access_token":"a%d","refresh_token":"r%d","email":%q,"sub":"s%d"}`,
			i+1, i+1, email, i+1,
		))
		a, err := store.UpsertFromOAuthJSON(ctx, raw, true)
		if err != nil {
			t.Fatal(err)
		}
		activeIDs = append(activeIDs, a.ID)
	}
	_, err := store.UpsertFromOAuthJSON(ctx,
		[]byte(`{"access_token":"ad","refresh_token":"rd","email":"d@x.ai","sub":"s4"}`),
		false)
	if err != nil {
		t.Fatal(err)
	}

	p := &account.Picker{Store: store}
	var ids []string
	for i := 0; i < 3; i++ {
		a, err := p.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	if ids[0] == ids[1] && ids[1] == ids[2] {
		t.Fatal("expected rotation")
	}
	// Collect set of returned IDs; all must be from the three active ones.
	activeSet := map[string]struct{}{}
	for _, id := range activeIDs {
		activeSet[id] = struct{}{}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := activeSet[id]; !ok {
			t.Fatalf("picked non-active account %q", id)
		}
		seen[id] = true
	}

	// Full cycle of 6 should cover all actives and never the disabled.
	for i := 0; i < 3; i++ {
		a, err := p.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
		seen[a.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected all 3 active accounts over 6 picks, got %d unique: %v", len(seen), ids)
	}
	// Round-robin: first 3 should equal next 3 in order.
	for i := 0; i < 3; i++ {
		if ids[i] != ids[i+3] {
			t.Fatalf("round-robin mismatch: ids=%v", ids)
		}
	}
}

func TestPickerNextNoneActive(t *testing.T) {
	store := openTestStore(t)
	p := &account.Picker{Store: store}
	_, err := p.Next(context.Background())
	if err == nil {
		t.Fatal("expected error when no active accounts")
	}
}

func TestPickerMarkErrorAndActive(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	a, err := store.UpsertFromOAuthJSON(ctx,
		[]byte(`{"access_token":"a","refresh_token":"r","email":"a@x.ai","sub":"s1"}`),
		true)
	if err != nil {
		t.Fatal(err)
	}

	p := &account.Picker{Store: store}
	if err := p.MarkError(ctx, a.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "error" || got.ErrorMessage != "boom" {
		t.Fatalf("status=%q err=%q", got.Status, got.ErrorMessage)
	}

	// Marked error → no longer selectable.
	if _, err := p.Next(ctx); err == nil {
		t.Fatal("expected no active after MarkError")
	}

	if err := p.MarkActive(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" || got.ErrorMessage != "" {
		t.Fatalf("status=%q err=%q", got.Status, got.ErrorMessage)
	}

	picked, err := p.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if picked.ID != a.ID {
		t.Fatalf("picked %q want %q", picked.ID, a.ID)
	}
}
