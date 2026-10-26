// SPDX-License-Identifier: AGPL-3.0-only

package managercmd

import (
	"testing"

	"go.miloapis.com/instrumentation/instrumenttest"
)

func TestInstrumentationAdopted(t *testing.T) {
	instrumenttest.AssertAdopted(t,
		instrumenttest.SkipDir("internal/extensionserver"),
		instrumenttest.SkipDir("internal/cmd/manager/secondary"),
	)
}

func TestMetricsLint(t *testing.T) {
	instrumenttest.Lint(t)
}
