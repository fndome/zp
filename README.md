# zp — Generic Request Batching Library for Go

`zp` reduces N individual operations into 1 batch call — excellent for amortizing
CGO overhead, database writes, or any high-latency downstream that benefits from
grouping.

```
Submit(T) → channel buffer → batcher accumulates → Processor([]T) → []R → dispatch to callers
```

## Install

```bash
go get github.com/fndome/zp
```

## Quickstart

```go
import "github.com/fndome/zp"

// 1. Define your batch processor
processor := func(ctx context.Context, batch []int64) ([]int64, error) {
    // hit database / CGO / remote service once for the whole batch
    return heavyCompute(batch), nil
}

// 2. Create a batcher: 100 items or 10ms timeout, whichever first
b := zp.NewBatcher(100, 10*time.Millisecond, processor)
defer b.Stop()

// 3. Submit from any goroutine — zero lock, channel-only
result, err := b.Submit(context.Background(), 42)
```

## API

```go
// Processor receives a batch of T, returns one R per T (must match length).
type Processor[T any, R any] func(ctx context.Context, batch []T) ([]R, error)

// NewBatcher creates a batcher with trigger threshold and max wait.
func NewBatcher[T, R any](batchSize int, maxWait time.Duration, p Processor[T, R]) *Batcher[T, R]

// Submit pushes a request into the batch. Blocks until the batch is processed.
func (b *Batcher[T, R]) Submit(ctx context.Context, input T) (R, error)

// Stop gracefully shuts down the batcher and drains unprocessed requests.
func (b *Batcher[T, R]) Stop()
```

## Examples

| Example | Directory | Description |
|---------|-----------|-------------|
| CGO batching | `example/go-in-zig/` | Go → CGO → Zig profit-split engine, `zig build run` |
| Game server | `example/game-update/` | Player state update: Submit only userId, processor queries latest DB state |

## Design

- **Zero-lock**: all coordination via channels, no mutex.
- **Panic-safe**: processor panics are captured and returned as errors to each submitter.
- **Stop-safe**: `Stop()` marks the batcher closed, `Submit()` returns `ErrBatcherClosed`.
- **Generic**: `Batcher[OrderRequest, ProfitResult]` is self-documenting — no `interface{}` casting.
- **Cancelable context**: processor receives the batcher's lifecycle context, canceled by `Stop()` — not the first request's context.

## When to Use

**Good fit:**
- CGO calls (each call ~100ns fixed overhead → batched: 100ns / batch_size)
- Database inserts/updates (one `INSERT ... VALUES (a),(b),(c)` vs N individual INSERTs)
- Remote API calls with per-request latency overhead

**Not a fit:**
- Calls that must happen immediately (real-time bidding, interactive UI)
- Operations where batch ordering matters and reordering is unacceptable
