package zp

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStopSubmitRace(t *testing.T) {
	for i := 0; i < 200; i++ {
		b := NewBatcher(4, 10*time.Millisecond, func(ctx context.Context, batch []int) ([]int, error) {
			return batch, nil
		})

		var wg sync.WaitGroup
		done := make(chan struct{})

		for j := 0; j < 20; j++ {
			wg.Add(1)
			go func(v int) {
				defer wg.Done()
				b.Submit(context.Background(), v)
			}(j)
		}

		go func() {
			b.Stop()
		}()

		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Submit blocked forever after Stop")
		}
	}
}

func TestBasicBatch(t *testing.T) {
	b := NewBatcher(3, 20*time.Millisecond, func(ctx context.Context, batch []int) ([]int, error) {
		out := make([]int, len(batch))
		for i, v := range batch {
			out[i] = v * 2
		}
		return out, nil
	})
	defer b.Stop()

	var wg sync.WaitGroup
	for i := 1; i <= 9; i++ {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			r, err := b.Submit(context.Background(), v)
			if err != nil {
				t.Errorf("Submit(%d) err: %v", v, err)
				return
			}
			if r != v*2 {
				t.Errorf("Submit(%d) = %d, want %d", v, r, v*2)
			}
		}(i)
	}
	wg.Wait()
}

func TestBatchSizeOneFlushesImmediately(t *testing.T) {
	b := NewBatcher(1, time.Hour, func(ctx context.Context, batch []int) ([]int, error) {
		out := make([]int, len(batch))
		for i, v := range batch {
			out[i] = v + 1
		}
		return out, nil
	})
	defer b.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r, err := b.Submit(ctx, 41)
	if err != nil {
		t.Fatalf("Submit err: %v", err)
	}
	if r != 42 {
		t.Fatalf("r = %d, want 42", r)
	}
}

func TestSubmitAfterStop(t *testing.T) {
	b := NewBatcher(2, 10*time.Millisecond, func(ctx context.Context, batch []int) ([]int, error) {
		return batch, nil
	})
	b.Stop()

	_, err := b.Submit(context.Background(), 1)
	if err != ErrBatcherClosed {
		t.Fatalf("err = %v, want ErrBatcherClosed", err)
	}
}

func TestStopCancelsInflightProcessor(t *testing.T) {
	started := make(chan struct{})
	b := NewBatcher(2, time.Hour, func(ctx context.Context, batch []int) ([]int, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(v int) {
			_, err := b.Submit(context.Background(), v)
			errCh <- err
		}(i)
	}

	<-started
	stopped := make(chan struct{})
	go func() {
		b.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not cancel in-flight processor")
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from canceled processor, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Submit did not return after Stop")
	}
}
