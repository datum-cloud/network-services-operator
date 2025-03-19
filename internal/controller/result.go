package controller

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Result struct {
	// Result contains the result of a Reconciler invocation.
	ctrl.Result

	// Err contains an error of a Reconciler invocation
	Err error

	// StopProcessing indicates that the caller should not continue processing and
	// let the Reconciler go to sleep without an explicit requeue, expecting a
	// Watch to trigger a future reconcilation call.
	StopProcessing bool

	syncStatus map[client.Object]client.Client
}

func (r *Result) AddStatusUpdate(c client.Client, obj client.Object) {
	if r.syncStatus == nil {
		r.syncStatus = make(map[client.Object]client.Client)
	}
	r.syncStatus[obj] = c
}

func (r Result) ShouldReturn() bool {
	return r.Err != nil || !r.Result.IsZero() || r.StopProcessing
}

func (r Result) Finish(ctx context.Context) (ctrl.Result, error) {
	if r.syncStatus != nil {

		var errs []error
		for obj, client := range r.syncStatus {
			if err := client.Status().Update(ctx, obj); err != nil {
				errs = append(errs, err)
			}
		}

		if len(errs) > 0 {
			r.Err = errors.Join(append([]error{r.Err}, errs...)...)
		}
	}

	return r.Result, r.Err
}
