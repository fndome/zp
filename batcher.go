package zp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Processor[T any, R any] func(ctx context.Context, batch []T) ([]R, error)

type pendingReq[T any, R any] struct {
	input  T
	result chan ResultWrapper[R]
}

type ResultWrapper[R any] struct {
	Value R
	Err   error
}

type Batcher[T any, R any] struct {
	pending   chan pendingReq[T, R]
	processor Processor[T, R]
	batchSize int
	maxWait   time.Duration

	closed   atomic.Bool
	stopOnce sync.Once
	stopChan chan struct{}
	wg       sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc
}

var (
	ErrBatcherClosed  = fmt.Errorf("batcher closed")
	ErrResultMismatch = fmt.Errorf("result count mismatch batch size")
)

func NewBatcher[T any, R any](batchSize int, maxWait time.Duration, processor Processor[T, R]) *Batcher[T, R] {
	if batchSize < 1 {
		panic("zp.NewBatcher: batchSize must be >= 1")
	}
	if maxWait <= 0 {
		panic("zp.NewBatcher: maxWait must be > 0")
	}

	ctx, cancel := context.WithCancel(context.Background())
	b := &Batcher[T, R]{
		pending:   make(chan pendingReq[T, R], batchSize*10),
		processor: processor,
		batchSize: batchSize,
		maxWait:   maxWait,
		stopChan:  make(chan struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}
	b.wg.Add(1)
	go b.run()
	return b
}

func (b *Batcher[T, R]) Submit(ctx context.Context, input T) (R, error) {
	if b.closed.Load() {
		var zero R
		return zero, ErrBatcherClosed
	}

	resultCh := make(chan ResultWrapper[R], 1)

	select {
	case b.pending <- pendingReq[T, R]{input: input, result: resultCh}:
	case <-b.stopChan:
		var zero R
		return zero, ErrBatcherClosed
	case <-ctx.Done():
		var zero R
		return zero, ctx.Err()
	}

	select {
	case res, ok := <-resultCh:
		if !ok {
			var zero R
			return zero, ErrBatcherClosed
		}
		return res.Value, res.Err
	case <-ctx.Done():
		var zero R
		return zero, ctx.Err()
	case <-b.stopChan:
		// closing: the request was either flushed before run() exited,
		// or it is stuck in pending and will never be processed.
		// wait for run() to finish, then check for a delivered result.
		b.wg.Wait()
		select {
		case res, ok := <-resultCh:
			if !ok {
				var zero R
				return zero, ErrBatcherClosed
			}
			return res.Value, res.Err
		default:
			var zero R
			return zero, ErrBatcherClosed
		}
	}
}

func (b *Batcher[T, R]) Stop() {
	b.stopOnce.Do(func() {
		b.closed.Store(true)
		b.cancel()
		close(b.stopChan)
	})
	b.wg.Wait()

	// drain requests that snuck past the closed check into the channel
	// before run() exited — close their result chans so Submit() unblocks
	for {
		select {
		case req := <-b.pending:
			close(req.result)
		default:
			return
		}
	}
}

func (b *Batcher[T, R]) run() {
	defer b.wg.Done()

	var batch []pendingReq[T, R]
	var timer *time.Timer

	for {
		// idle: wait for the first request
		if len(batch) == 0 {
			if timer != nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer = nil
			}

			select {
			case <-b.stopChan:
				return
			case req := <-b.pending:
				batch = append(batch, req)
				if len(batch) >= b.batchSize {
					b.flush(batch)
					batch = nil
					continue
				}
				timer = time.NewTimer(b.maxWait)
				continue
			}
		}

		// accumulating: wait for more requests or timeout
		select {
		case <-b.stopChan:
			if len(batch) > 0 {
				b.flush(batch)
			}
			return

		case req := <-b.pending:
			batch = append(batch, req)

			if len(batch) >= b.batchSize {
				if timer != nil && !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				b.flush(batch)
				batch = nil
				timer = nil
			}

		case <-timer.C:
			b.flush(batch)
			batch = nil
			timer = nil
		}
	}
}

func (b *Batcher[T, R]) flush(batch []pendingReq[T, R]) {
	if len(batch) == 0 {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			panicErr := fmt.Errorf("processor panic: %v", r)
			for _, req := range batch {
				select {
				case req.result <- ResultWrapper[R]{Err: panicErr}:
				default:
				}
			}
		}
	}()

	inputs := make([]T, len(batch))
	for i, req := range batch {
		inputs[i] = req.input
	}

	results, err := b.processor(b.ctx, inputs)

	if err != nil {
		for _, req := range batch {
			select {
			case req.result <- ResultWrapper[R]{Err: err}:
			default:
			}
		}
		return
	}

	if len(results) != len(batch) {
		for _, req := range batch {
			select {
			case req.result <- ResultWrapper[R]{Err: ErrResultMismatch}:
			default:
			}
		}
		return
	}

	for i, req := range batch {
		select {
		case req.result <- ResultWrapper[R]{Value: results[i]}:
		default:
		}
	}
}
