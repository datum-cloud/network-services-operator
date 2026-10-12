package controller

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/util/status"
)

type Result struct {
	// Result contains the result of a Reconciler invocation.
	ctrl.Result

	// Err contains an error of a Reconciler invocation
	Err error

	// StopProcessing indicates that the caller should not continue processing and
	// let the Reconciler go to sleep without an explicit requeue, expecting a
	// Watch to trigger a future reconciliation call.
	StopProcessing bool

	syncStatus map[client.Object]statusSave
}

// statusSave is how Complete saves one object's status. An empty fieldOwner
// saves it with an Update locked to the resourceVersion the object was read
// at; a field owner saves it with a merge patch from the stored status.
type statusSave struct {
	client     client.Client
	fieldOwner string
}

func (r *Result) Merge(other Result) Result {
	if other.Err != nil {
		r.Err = errors.Join(r.Err, other.Err)
	}
	if other.Result != (ctrl.Result{}) {
		r.Result = other.Result
	}
	if other.StopProcessing {
		r.StopProcessing = true
	}
	if other.syncStatus != nil {
		if r.syncStatus == nil {
			r.syncStatus = make(map[client.Object]statusSave)
		}
		for k, v := range other.syncStatus {
			r.syncStatus[k] = v
		}
	}

	return *r
}

// AddStatusUpdate saves obj's status locked to the read it was computed from,
// for a status that several reconciles write into one list.
func (r *Result) AddStatusUpdate(c client.Client, obj client.Object) {
	r.addStatusSave(obj, statusSave{client: c})
}

// AddStatusPatch saves obj's status with a merge patch under fieldOwner, for
// a status that only this controller writes.
func (r *Result) AddStatusPatch(c client.Client, obj client.Object, fieldOwner string) {
	r.addStatusSave(obj, statusSave{client: c, fieldOwner: fieldOwner})
}

func (r *Result) addStatusSave(obj client.Object, save statusSave) {
	if r.syncStatus == nil {
		r.syncStatus = make(map[client.Object]statusSave)
	}
	r.syncStatus[obj] = save
}

func (r Result) ShouldReturn() bool {
	return r.Err != nil || !r.IsZero() || r.StopProcessing
}

func (r Result) Complete(ctx context.Context) (ctrl.Result, error) {
	if r.syncStatus != nil {
		var errs []error
		for obj, save := range r.syncStatus {
			stored, needsWrite, err := storedStatus(ctx, save.client, obj)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !needsWrite {
				continue
			}
			if err := save.write(ctx, obj, stored); err != nil {
				if r.Err == nil && apierrors.IsConflict(err) {
					r.RequeueAfter = 1 * time.Second
				} else {
					errs = append(errs, err)
				}
			}
		}

		if len(errs) > 0 {
			r.Err = errors.Join(append([]error{r.Err}, errs...)...)
		}
	}

	return r.Result, r.Err
}

// storedStatus reads the object desired was computed for and reports whether
// its status differs. An object it cannot read is written, so the write
// reports why.
func storedStatus(ctx context.Context, c client.Client, desired client.Object) (stored client.Object, needsWrite bool, err error) {
	live := desired.DeepCopyObject().(client.Object)
	if err := c.Get(ctx, client.ObjectKeyFromObject(desired), live); err != nil {
		return nil, true, nil
	}
	liveStatus, err := statusContent(live)
	if err != nil {
		return nil, false, err
	}
	desiredStatus, err := statusContent(desired)
	if err != nil {
		return nil, false, err
	}
	return live, !equality.Semantic.DeepEqual(liveStatus, desiredStatus), nil
}

func (s statusSave) write(ctx context.Context, obj, stored client.Object) error {
	if s.fieldOwner == "" {
		return s.client.Status().Update(ctx, obj)
	}
	return status.Patch(ctx, s.client, obj, stored, s.fieldOwner)
}

func statusContent(obj client.Object) (any, error) {
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return content["status"], nil
}
