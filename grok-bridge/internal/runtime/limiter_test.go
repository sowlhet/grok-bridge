package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterUnlimited(t *testing.T) {
	l := NewLimiter(0, 0)
	rel, err := l.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	rel()
}

func TestLimiterGlobalBlocks(t *testing.T) {
	l := NewLimiter(1, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	rel1, err := l.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer rel1()

	_, err = l.Acquire(ctx, "b")
	if err == nil {
		t.Fatal("expected wait timeout under global limit 1")
	}
}

func TestLimiterPerAccount(t *testing.T) {
	l := NewLimiter(0, 1)
	rel1, err := l.Acquire(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	// different account should pass
	rel2, err := l.Acquire(context.Background(), "acc2")
	if err != nil {
		t.Fatal(err)
	}
	rel2()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err = l.Acquire(ctx, "acc1")
	if err == nil {
		t.Fatal("expected per-account block")
	}
	rel1()

	// after release, should acquire
	rel3, err := l.Acquire(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	rel3()
}

func TestLimiterConcurrentRelease(t *testing.T) {
	l := NewLimiter(4, 2)
	var wg sync.WaitGroup
	var okCount atomic.Int64
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			rel, err := l.Acquire(ctx, "same")
			if err != nil {
				return
			}
			okCount.Add(1)
			time.Sleep(5 * time.Millisecond)
			rel()
		}(i)
	}
	wg.Wait()
	if okCount.Load() == 0 {
		t.Fatal("expected some acquisitions")
	}
}
