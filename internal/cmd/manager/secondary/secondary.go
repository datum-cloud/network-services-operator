// SPDX-License-Identifier: AGPL-3.0-only

package secondary

import (
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func MetricsServerOptions() metricsserver.Options {
	return metricsserver.Options{BindAddress: "0"}
}
