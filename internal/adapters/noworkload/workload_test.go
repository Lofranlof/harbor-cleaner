package noworkload

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveImageRefsAlwaysReturnsEmptySet(t *testing.T) {
	refs, err := New().LiveImageRefs(context.Background())

	require.NoError(t, err)
	assert.Empty(t, refs)
}
