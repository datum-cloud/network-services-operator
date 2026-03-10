package words

import (
	"fmt"
	"testing"
)

func TestWordsAndEntropy(t *testing.T) {
	suffix := ".datum.local"
	seed := "00000000-0000-0000-0000-000000000001"

	first := WordsAndEntropy(suffix, seed)
	second := WordsAndEntropy(suffix, seed)
	if first != second {
		t.Fatalf("expected deterministic output for same seed, got %q and %q", first, second)
	}

	otherSeed := "00000000-0000-0000-0000-000000000002"
	third := WordsAndEntropy(suffix, otherSeed)
	if first == third {
		t.Fatalf("expected different output for different seeds, got %q", first)
	}
}

// This is not an exhaustive collision test, but a quick check that
// runs in a couple of seconds to make sure we haven't introduced an
// obvious entropy bug.
//
// In actuality this starts generating collisions around 8.8M
// iterations, simply because an incrementing integer isn't random
// enough.
//
// In production use with randomized namespace and project names
// created at different times across different machines, this should
// be more than sufficient.
func TestNoCollisions(t *testing.T) {
	const n = 100_000

	set := make(map[string]bool)

	for i := 0; i < n; i++ {
		suffix := "--test"
		seedStr := fmt.Sprintf("%d", i)
		hostname := WordsAndEntropy(suffix, seedStr)

		if set[hostname] {
			t.Errorf("collision detected at index %d: %s", i, hostname)
		}
		set[hostname] = true
	}

	if len(set) != n {
		t.Errorf("expected set length %d, got %d", n, len(set))
	}
}
