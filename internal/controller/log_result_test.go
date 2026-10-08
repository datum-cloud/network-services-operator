// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/go-logr/logr/funcr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestLogProcessed(t *testing.T) {
	tests := []struct {
		name   string
		result controllerutil.OperationResult
		want   int
	}{
		{"unchanged is suppressed at default level", controllerutil.OperationResultNone, 0},
		{"created is emitted", controllerutil.OperationResultCreated, 1},
		{"updated is emitted", controllerutil.OperationResultUpdated, 1},
		{"updated status is emitted", controllerutil.OperationResultUpdatedStatus, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := 0
			logger := funcr.New(func(_, _ string) { lines++ }, funcr.Options{Verbosity: 0})
			logProcessed(logger, tt.result, "processed", "result", tt.result)
			if lines != tt.want {
				t.Fatalf("got %d lines, want %d", lines, tt.want)
			}
		})
	}
}

func TestLogProcessedUnchangedEmittedAtVerbose(t *testing.T) {
	lines := 0
	logger := funcr.New(func(_, _ string) { lines++ }, funcr.Options{Verbosity: 1})
	logProcessed(logger, controllerutil.OperationResultNone, "processed")
	if lines != 1 {
		t.Fatalf("got %d lines, want 1", lines)
	}
}
