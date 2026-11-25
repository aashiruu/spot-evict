package lease

import (
"context"
"errors"
"testing"
"time"
)

func TestFencingPreventsSplitBrainAfterLeaseExpiry(t *testing.T) {
ctx := context.Background()
mgr := NewMemoryManager()

// W1 acquires lease with token=1
l1, err := mgr.Acquire(ctx, "job-1", "w1", 80*time.Millisecond)
if err != nil || l1.FencingToken != 1 {
t.Fatalf("expected W1 token=1, got %+v err=%v", l1, err)
}

// While W1's lease is active, W2 cannot steal it
if _, err := mgr.Acquire(ctx, "job-1", "w2", 80*time.Millisecond); !errors.Is(err, ErrLeaseHeld) {
t.Fatalf("expected ErrLeaseHeld for W2 while W1 active, got %v", err)
}

// Simulate W1 freezing (SIGSTOP) past TTL
time.Sleep(100 * time.Millisecond)

// W2 acquires expired lease and gets monotonic fencing_token=2
l2, err := mgr.Acquire(ctx, "job-1", "w2", 5*time.Second)
if err != nil || l2.FencingToken != 2 {
t.Fatalf("expected W2 token=2, got %+v err=%v", l2, err)
}

// W1 wakes up (SIGCONT) and tries to renew/write with stale token=1 -> fenced out!
if _, err := mgr.VerifyAndRenew(ctx, "job-1", "w1", l1.FencingToken, 5*time.Second); !errors.Is(err, ErrFencedOut) {
t.Fatalf("expected ErrFencedOut for stale W1, got %v", err)
}
}
