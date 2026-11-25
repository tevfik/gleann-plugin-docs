package engine

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// The output budget is shared by every buffer of a conversion. Once pages or
// fragments are produced in parallel, take/give/Err race without a lock; run
// with -race to catch it.
func TestOutBudgetConcurrentTakeGive(t *testing.T) {
	const workers = 8
	const rounds = 200
	ob := newOutBudget()
	limit := ob.limit

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := newMDBuilderOn(ob)
			for j := 0; j < rounds; j++ {
				if _, err := m.WriteString("some output text"); err != nil {
					t.Errorf("write under the budget failed: %v", err)
					return
				}
				_ = m.Err()
				_ = remainingBudget(m)
				m.Discard() // refund: the fragment was merged into its parent
			}
		}()
	}
	wg.Wait()

	if err := ob.stickyErr(); err != nil {
		t.Fatalf("sticky error after net-zero writes: %v", err)
	}
	if got := ob.remaining(); got != limit {
		t.Fatalf("remaining = %d, want the full limit %d (every write was refunded)", got, limit)
	}
}

// Concurrent charges must never let the total pass the limit: once the budget
// is exhausted the error is sticky and remaining stays >= 0.
func TestOutBudgetConcurrentOverdrawIsSticky(t *testing.T) {
	ob := &outBudget{left: 100, limit: 100}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if err := ob.take(7); err != nil {
					if !errors.Is(err, ErrTooLarge) {
						t.Errorf("got %v, want ErrTooLarge", err)
					}
					return
				}
				mu.Lock()
				accepted += 7
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted > 100 {
		t.Fatalf("accepted %d bytes past a 100 byte budget", accepted)
	}
	if ob.remaining() < 0 {
		t.Fatalf("remaining went negative: %d", ob.remaining())
	}
	if ob.stickyErr() == nil {
		t.Fatal("overdraw left no sticky error")
	}
}

// The zip read budget is shared by every member read of a conversion.
func TestZipBudgetConcurrentCharge(t *testing.T) {
	const workers = 8
	const chunk = 1024
	b := &zipBudget{left: 1 << 20}
	var wg sync.WaitGroup
	var refused atomic.Bool
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if err := b.charge(chunk); err != nil {
					if !errors.Is(err, ErrTooLarge) {
						t.Errorf("got %v, want ErrTooLarge", err)
					}
					refused.Store(true)
					return
				}
			}
		}()
	}
	wg.Wait()
	if !refused.Load() {
		t.Fatal("4 MiB of charges against a 1 MiB budget never refused")
	}
	// Each worker may overshoot once before it sees the refusal.
	if b.left < -workers*chunk {
		t.Fatalf("zip budget underflowed: %d", b.left)
	}
}
