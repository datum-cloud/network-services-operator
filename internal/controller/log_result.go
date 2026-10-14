// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func logProcessed(logger logr.Logger, result controllerutil.OperationResult, msg string, keysAndValues ...any) {
	if result == controllerutil.OperationResultNone {
		logger = logger.V(1)
	}
	logger.Info(msg, keysAndValues...)
}
