package parity

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubFetcher returns a fixed programmed-set JSON body (or error) for the
// programmed-set path. Models one ext-server replica.
type stubFetcher struct {
	buildID int
	err     error
}

func (s stubFetcher) Fetch(_ context.Context, path string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if path != ProgrammedSetPath {
		return nil, fmt.Errorf("unexpected path %s", path)
	}
	body := fmt.Sprintf(`{"buildID":%d,"keys":{"waf_route":["k%d"]},"counts":{"waf_route":1}}`,
		s.buildID, s.buildID)
	return []byte(body), nil
}

// TestFetchExpectedFromMany_PicksHighestBuildID is the core multi-replica rule:
// EG pins its gRPC connection to one replica, so only that pod has the real
// (highest-BuildID) programmed-set; the idle replica sits at 0.
func TestFetchExpectedFromMany_PicksHighestBuildID(t *testing.T) {
	fetchers := map[string]Fetcher{
		"ext-replica-a": stubFetcher{buildID: 0}, // idle replica
		"ext-replica-b": stubFetcher{buildID: 7}, // active replica
	}
	src, errs, err := FetchExpectedFromMany(context.Background(), fetchers)
	require.NoError(t, err)
	assert.Empty(t, errs)
	assert.Equal(t, "ext-replica-b", src.Replica, "must pick the active (highest-BuildID) replica")
	assert.Equal(t, uint64(7), src.Expected.BuildID)
	assert.Equal(t, []string{"k7"}, src.Expected.Keys[FamilyWAFRoute])
}

// TestFetchExpectedFromMany_OneUnreachable: one replica down is not fatal as
// long as another answers; the error is surfaced, not returned.
func TestFetchExpectedFromMany_OneUnreachable(t *testing.T) {
	fetchers := map[string]Fetcher{
		"ext-replica-a": stubFetcher{err: errors.New("connection refused")},
		"ext-replica-b": stubFetcher{buildID: 3},
	}
	src, errs, err := FetchExpectedFromMany(context.Background(), fetchers)
	require.NoError(t, err)
	assert.Equal(t, "ext-replica-b", src.Replica)
	require.Contains(t, errs, "ext-replica-a")
	assert.ErrorContains(t, errs["ext-replica-a"], "connection refused")
}

// TestFetchExpectedFromMany_AllFail returns an error only when no replica answers.
func TestFetchExpectedFromMany_AllFail(t *testing.T) {
	fetchers := map[string]Fetcher{
		"a": stubFetcher{err: errors.New("down")},
		"b": stubFetcher{err: errors.New("down")},
	}
	_, errs, err := FetchExpectedFromMany(context.Background(), fetchers)
	require.Error(t, err)
	assert.Len(t, errs, 2)
}

// TestFetchExpectedFromMany_TieResolvesDeterministically: both at 0 (no build
// anywhere yet) resolves to the first replica in sorted order, returning a
// well-formed empty Expected rather than an error.
func TestFetchExpectedFromMany_TieResolvesDeterministically(t *testing.T) {
	fetchers := map[string]Fetcher{
		"ext-z": stubFetcher{buildID: 0},
		"ext-a": stubFetcher{buildID: 0},
	}
	src, _, err := FetchExpectedFromMany(context.Background(), fetchers)
	require.NoError(t, err)
	assert.Equal(t, "ext-a", src.Replica, "ties resolve to the first replica in sorted order")
	assert.Equal(t, uint64(0), src.Expected.BuildID)
}

func TestFetchExpectedFromMany_Empty(t *testing.T) {
	_, _, err := FetchExpectedFromMany(context.Background(), nil)
	require.Error(t, err)
}
