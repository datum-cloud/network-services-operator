// SPDX-License-Identifier: AGPL-3.0-only

// Package status saves a status that one controller writes, without a
// resourceVersion, so a write to any other part of the object does not fail
// it. The save is a JSON merge patch through the status subresource, computed
// with controller-runtime's client.MergeFrom on the two statuses alone, as
// Cluster API's util/patch helper does for its status patch.
package status

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const statusKey = "status"

// Patch saves obj's status as a merge patch from stored, the object as it was
// read, under fieldOwner, and updates obj from the response. The patch holds
// what changed between the two statuses and nothing else: no resourceVersion,
// whatever else differs between obj and stored. A nil stored saves all of
// obj's status. Errors from the API server are returned unwrapped.
func Patch(ctx context.Context, c client.Client, obj, stored client.Object, fieldOwner string) error {
	after, err := statusOnly(obj)
	if err != nil {
		return err
	}
	before := &unstructured.Unstructured{Object: map[string]any{}}
	if stored != nil {
		if before, err = statusOnly(stored); err != nil {
			return err
		}
	}
	data, err := client.MergeFrom(before).Data(after)
	if err != nil {
		return fmt.Errorf("failed computing the status patch: %w", err)
	}
	return c.Status().Patch(ctx, obj, client.RawPatch(types.MergePatchType, data), client.FieldOwner(fieldOwner))
}

// statusOnly returns obj cut down to its status.
func statusOnly(obj client.Object) (*unstructured.Unstructured, error) {
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("failed reading the status of %T: %w", obj, err)
	}
	cut := map[string]any{}
	if status, ok := content[statusKey]; ok {
		cut[statusKey] = status
	}
	return &unstructured.Unstructured{Object: cut}, nil
}
