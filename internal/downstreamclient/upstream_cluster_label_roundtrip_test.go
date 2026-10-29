package downstreamclient

import (
	"math/rand"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

func TestUpstreamClusterLabelRoundTrips(t *testing.T) {
	cases := []struct {
		name       string
		cluster    string
		registered string
	}{
		{"plain", "example-org", "example-org"},
		{"generated suffix", "project-4kq7x", "project-4kq7x"},
		{"user project", "my-project-a1b2c3", "my-project-a1b2c3"},
		{"longest project name Milo admits", strings.Repeat("a", 55), strings.Repeat("a", 55)},
		{"name starting with the prefix", "cluster-x", "cluster-x"},
		{"multi-segment", "org/my-project", "org/my-project"},
		{"legacy leading slash", "/example-org", "example-org"},
		{"legacy leading slash, user project", "/my-project-a1b2c3", "my-project-a1b2c3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			label := UpstreamClusterNameLabelValue(tc.cluster)
			if errs := validation.IsValidLabelValue(label); len(errs) > 0 {
				t.Errorf("label %q for %q is not a valid label value: %v", label, tc.cluster, errs)
			}
			if got := UpstreamClusterNameFromLabel(label); got != tc.registered {
				t.Errorf("%q -> %q -> %q, want %q", tc.cluster, label, got, tc.registered)
			}
		})
	}
}

func TestLegacyUpstreamClusterLabelsDecode(t *testing.T) {
	for label, want := range map[string]string{
		"cluster-example-org":        "example-org",
		"cluster-_example-org":       "example-org",
		"cluster-_my-project-a1b2c3": "my-project-a1b2c3",
		"cluster-project-4kq7x":      "project-4kq7x",
	} {
		if got := UpstreamClusterNameFromLabel(label); got != want {
			t.Errorf("UpstreamClusterNameFromLabel(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestEveryProjectNameRoundTrips(t *testing.T) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789-"
	r := rand.New(rand.NewSource(1))
	for range 10000 {
		b := make([]byte, 1+r.Intn(55))
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
		}
		name := strings.Trim(string(b), "-")
		if name == "" || len(validation.IsDNS1123Label(name)) > 0 {
			continue
		}
		label := UpstreamClusterNameLabelValue(name)
		if errs := validation.IsValidLabelValue(label); len(errs) > 0 {
			t.Fatalf("label %q for %q is not a valid label value: %v", label, name, errs)
		}
		if got := UpstreamClusterNameFromLabel(label); got != name {
			t.Fatalf("%q -> %q -> %q", name, label, got)
		}
	}
}
