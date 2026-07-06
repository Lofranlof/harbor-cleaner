package utils

import (
	"context"
	"sync"
)

// Gather runs fn once per item, each in its own goroutine (unbounded fan-out -
// callers needing a concurrency cap should use FetchConcurrently or their own
// worker pool instead), and returns the results in the same order as items.
// Each goroutine writes to its own index of the results slice, so no lock is
// needed there; only errors are communicated back over a channel.
//
// If any call errors, Gather returns the first error it observes and a nil
// result slice - the other goroutines still run to completion first.
func Gather[I, O any](ctx context.Context, items []I, fn func(context.Context, I) (O, error)) ([]O, error) {
	results := make([]O, len(items))
	errsCh := make(chan error, len(items))

	var wg sync.WaitGroup
	wg.Add(len(items))
	for i, item := range items {
		go func() {
			defer wg.Done()
			result, err := fn(ctx, item)
			if err != nil {
				errsCh <- err
				return
			}
			results[i] = result
		}()
	}
	wg.Wait()
	close(errsCh)

	for err := range errsCh {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}
