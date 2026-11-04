// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// plane is one of the package's API servers, with a client for it.
type plane struct {
	client client.Client
	config *rest.Config
}

// The planes are started once for the package, at first use, and TestMain
// stops them, as kubebuilder's suite_test.go starts one environment for a
// package. An apiserver takes about 3 s to start, so one per test came to over
// half of the package's time. Every scenario names its own namespaces, so
// sharing them isolates nothing less.
var planes = sync.OnceValues(func() ([]plane, error) {
	testScheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(testScheme); err != nil {
		return nil, err
	}
	if err := networkingv1alpha.AddToScheme(testScheme); err != nil {
		return nil, err
	}
	if err := locationsv1alpha1.AddToScheme(testScheme); err != nil {
		return nil, err
	}

	started := make([]plane, 0, 2)
	for range 2 {
		env := &envtest.Environment{
			CRDDirectoryPaths: []string{
				filepath.Join("..", "..", "config", "crd", "bases"),
				filepath.Join("..", "..", "bin", "crds", "locations"),
			},
			ErrorIfCRDPathMissing: true,
		}
		cfg, err := env.Start()
		if err != nil {
			return nil, err
		}
		planeEnvironments = append(planeEnvironments, env)

		cl, err := client.New(cfg, client.Options{Scheme: testScheme})
		if err != nil {
			return nil, err
		}
		started = append(started, plane{client: cl, config: cfg})
	}

	return started, nil
})

var planeEnvironments []*envtest.Environment

func TestMain(m *testing.M) {
	code := m.Run()
	for _, env := range planeEnvironments {
		_ = env.Stop()
	}
	os.Exit(code)
}

// sharedPlanes returns the package's two planes, or skips a test run without
// envtest's binaries.
func sharedPlanes(t *testing.T) []plane {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}

	started, err := planes()
	require.NoError(t, err)
	return started
}

func startPlanes(t *testing.T) (cellPlane, hubPlane client.Client) {
	t.Helper()
	started := sharedPlanes(t)
	return started[0].client, started[1].client
}

// scenarioSuffixLength leaves a scenario 17 characters for a prefix in front of
// its suffix: a namespace's name is a DNS label of at most 63.
const scenarioSuffixLength = 46

// scenarios numbers the scenarios of this test binary.
var scenarios atomic.Uint64

// scenarioSuffix returns t's name as a DNS label, cut to fit, ending in a
// number no other scenario in this test binary holds. The planes outlive a
// test, so a name taken from t's name alone meets the name of another test
// that starts the same way, or its own under -count.
func scenarioSuffix(t *testing.T) string {
	t.Helper()
	number := "-" + strconv.FormatUint(scenarios.Add(1), 10)
	name := sanitizeName(strings.ToLower(t.Name()))
	if room := scenarioSuffixLength - len(number); len(name) > room {
		name = name[:room]
	}
	return name + number
}

// Two tests whose names share their start, and one test run twice as under
// -count, still get suffixes of their own, each fitting a namespace's name
// behind the longest prefix.
func TestScenarioSuffixesAreUniqueAndFit(t *testing.T) {
	const longestPrefix = "proj-unfederated-"
	shared := strings.Repeat("a-shared-start-", 4)
	seen := map[string]bool{}
	for _, name := range []string{shared + "one", shared + "two", "short"} {
		t.Run(name, func(t *testing.T) {
			for range 2 {
				suffix := scenarioSuffix(t)
				require.Empty(t, validation.IsDNS1123Label(longestPrefix+suffix), suffix)
				require.False(t, seen[suffix], "%s is taken", suffix)
				seen[suffix] = true
			}
		})
	}
}

func sanitizeName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
