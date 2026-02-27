package words

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestWordsAndEntropy(t *testing.T) {
	suffix := ".datum.local"

	fmt.Println("Example hostnames:")

	for i := 0; i < 5; i++ {
		randomString := WordsAndEntropy(suffix, fmt.Sprintf("%d", time.Now().UnixNano()))
		fmt.Printf("%s\n", randomString)
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
	const n = 1_000_000

	set := make(map[string]bool)

	for i := 0; i < n; i++ {
		suffix := "--test"
		seedStr := fmt.Sprintf("%d", i)
		hostname := WordsAndEntropy(suffix, seedStr)

		if set[hostname] {
			t.Errorf("collision detected at index %d: %s", i, hostname)
		}
		set[hostname] = true

		if i%100_000 == 0 {
			os.Stdout.WriteString(".")
		}
	}

	fmt.Println()

	if len(set) != n {
		t.Errorf("expected set length %d, got %d", n, len(set))
	}
}
