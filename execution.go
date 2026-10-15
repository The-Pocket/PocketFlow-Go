package pocketflow

import (
	"fmt"
	"sync"
	"time"
)

func validateRetry(attempts int, wait time.Duration) {
	if attempts < 1 {
		panic("maxRetries must be at least 1")
	}
	if wait < 0 {
		panic("retry wait cannot be negative")
	}
}

func waitRetry(ctx *PfContext, wait time.Duration) error {
	if wait <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func retryExec(ctx *PfContext, attempts int, wait time.Duration,
	exec func(*PfContext) (any, error), fallback func(*PfContext, error) (any, error),
) (any, error) {
	if attempts < 1 || wait < 0 {
		return nil, fmt.Errorf("invalid retry settings: attempts=%d wait=%s", attempts, wait)
	}
	var lastErr error
	var attemptCtx *PfContext
	for index := 0; index < attempts; index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ctx.retry = index
		attemptCtx = ctx.withRetry(index)
		result, err := exec(attemptCtx)
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		if err == nil {
			return result, nil
		}
		lastErr = err
		if index < attempts-1 {
			if err := waitRetry(ctx, wait); err != nil {
				return nil, err
			}
		}
	}
	if fallback == nil {
		return nil, lastErr
	}
	result, err := fallback(attemptCtx, lastErr)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, newPocketFlowError(fmt.Sprintf("Fallback failed after %d attempts", attempts), err)
	}
	return result, nil
}

// gather returns the first completed error without canceling siblings, matching
// asyncio.gather. A positive concurrency limit also bounds worker goroutines.
func gather(ctx *PfContext, count, concurrency int, run func(int) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	workerCount := count
	if concurrency > 0 && concurrency < workerCount {
		workerCount = concurrency
	}
	jobs := make(chan int)
	firstError := make(chan error, 1)
	done := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		if ctx.Err() != nil {
			break
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := run(index); err != nil {
					// The caller may already have returned. Keep draining jobs
					// without blocking on later errors or canceling siblings.
					select {
					case firstError <- err:
					default:
					}
				}
			}
		}()
	}
	go func() {
	dispatch:
		for index := 0; index < count; index++ {
			select {
			case <-ctx.Done():
				break dispatch
			case jobs <- index:
			}
		}
		close(jobs)
		workers.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-firstError:
		return err
	case <-done:
		// Completion can race with the first error notification.
		select {
		case err := <-firstError:
			return err
		default:
			return ctx.Err()
		}
	}
}
