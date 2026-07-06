package utils

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Results must come back in the same order as items, even though the
// goroutines that produce them finish in the opposite order.
func TestGather_PreservesOrder(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	fn := func(_ context.Context, n int) (int, error) {
		time.Sleep(time.Duration(len(items)-n) * 10 * time.Millisecond)
		return n * n, nil
	}

	results, err := Gather(context.Background(), items, fn)

	require.NoError(t, err)
	assert.Equal(t, []int{1, 4, 9, 16, 25}, results)
}

func TestGather_ReturnsErrorFromAnyItem(t *testing.T) {
	boom := errors.New("boom")
	fn := func(_ context.Context, n int) (int, error) {
		if n == 3 {
			return 0, boom
		}
		return n, nil
	}

	results, err := Gather(context.Background(), []int{1, 2, 3, 4}, fn)

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Nil(t, results)
}

// Items must run concurrently, not one after another - N items sleeping D
// each should take about D total, not N*D.
func TestGather_RunsItemsConcurrently(t *testing.T) {
	const n = 10
	const perItemSleep = 50 * time.Millisecond
	items := make([]int, n)
	fn := func(_ context.Context, _ int) (int, error) {
		time.Sleep(perItemSleep)
		return 0, nil
	}

	start := time.Now()
	_, err := Gather(context.Background(), items, fn)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, perItemSleep*n/2)
}
