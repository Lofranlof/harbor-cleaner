package utils

import (
	"context"
	"errors"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pages 1..5 carry data, page 6+ is the "no more data" sentinel.
func TestFetchConcurrently_CollectsAllPages(t *testing.T) {
	fn := func(_ context.Context, page int64) (int, error) {
		if page <= 5 {
			return int(page), nil
		}
		return 0, nil
	}
	isDone := func(v int) bool { return v == 0 }

	results, errs := FetchConcurrently(context.Background(), 3, fn, isDone, 5*time.Second)

	require.Empty(t, errs)
	sort.Ints(results)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, results)
}

// The page that satisfies isDone must not itself end up in the results.
func TestFetchConcurrently_ExcludesDoneSentinelFromResults(t *testing.T) {
	fn := func(_ context.Context, page int64) (int, error) {
		if page <= 2 {
			return int(page), nil
		}
		return -1, nil // sentinel for "done", must never appear in results
	}
	isDone := func(v int) bool { return v == -1 }

	results, errs := FetchConcurrently(context.Background(), 1, fn, isDone, 5*time.Second)

	require.Empty(t, errs)
	sort.Ints(results)
	assert.Equal(t, []int{1, 2}, results)
}

// Never more than numWorkers calls to fn should be in flight at once.
func TestFetchConcurrently_RespectsWorkerCount(t *testing.T) {
	const numWorkers = 4
	var inFlight, maxInFlight int64

	fn := func(_ context.Context, page int64) (int, error) {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			max := atomic.LoadInt64(&maxInFlight)
			if cur <= max || atomic.CompareAndSwapInt64(&maxInFlight, max, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		atomic.AddInt64(&inFlight, -1)
		if page > 30 {
			return 0, nil
		}
		return 1, nil
	}
	isDone := func(v int) bool { return v == 0 }

	_, errs := FetchConcurrently(context.Background(), numWorkers, fn, isDone, 10*time.Second)

	require.Empty(t, errs)
	assert.LessOrEqual(t, atomic.LoadInt64(&maxInFlight), int64(numWorkers))
}

// Once one worker gives up with a permanent error, the other workers must stop
// too instead of paginating forever - otherwise FetchConcurrently only returns
// once the outer timeout fires, no matter how quickly the real error surfaced.
func TestFetchConcurrently_PropagatesErrorAndCancelsOthers(t *testing.T) {
	const outerTimeout = 40 * time.Second
	permErr := errors.New("permanent failure")

	fn := func(_ context.Context, page int64) (int, error) {
		if page == 1 {
			return 0, permErr
		}
		return int(page), nil // always "has more data" - would loop forever without cancellation
	}
	isDone := func(int) bool { return false }

	start := time.Now()
	_, errs := FetchConcurrently(context.Background(), 3, fn, isDone, outerTimeout)
	elapsed := time.Since(start)

	require.NotEmpty(t, errs)
	assert.ErrorIs(t, errs[0], permErr)
	assert.Less(t, elapsed, outerTimeout/2,
		"other workers kept running instead of stopping once a permanent error was found")
}
