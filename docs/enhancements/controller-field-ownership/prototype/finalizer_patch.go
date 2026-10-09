//go:build prototype

/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package prototype

// addFinalizerPatch and removeFinalizerPatch are copied unchanged from
// kubernetes-csi/external-attacher v4.13.0, pkg/controller/util.go L229-L294:
// https://github.com/kubernetes-csi/external-attacher/blob/v4.13.0/pkg/controller/util.go#L229-L294

import (
	"encoding/json"
	"fmt"
)

// addFinalizerPatch creates a JSON Patch (RFC 6902) that appends a single
// finalizer to the metadata.finalizers array without replacing the whole list.
// This avoids a race where a merge-patch based on a stale informer cache
// inadvertently re-adds finalizers that other controllers have already removed,
// which causes 422 errors when the object is being deleted.
// See https://github.com/kubernetes-csi/external-provisioner/issues/1217.
func addFinalizerPatch(existingFinalizers []string, finalizerName string) ([]byte, error) {
	type patchOp struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}

	var ops []patchOp
	switch {
	case existingFinalizers == nil:
		ops = []patchOp{
			{Op: "test", Path: "/metadata/finalizers", Value: nil},
			{Op: "add", Path: "/metadata/finalizers", Value: []string{finalizerName}},
		}
	case len(existingFinalizers) == 0:
		ops = []patchOp{
			{Op: "test", Path: "/metadata/finalizers", Value: existingFinalizers},
			{Op: "replace", Path: "/metadata/finalizers", Value: []string{finalizerName}},
		}
	default:
		ops = []patchOp{
			{Op: "add", Path: "/metadata/finalizers/-", Value: finalizerName},
		}
	}
	return json.Marshal(ops)
}

// removeFinalizerPatch creates a JSON Patch (RFC 6902) that removes exactly one
// finalizer from metadata.finalizers by index, after verifying its value with a
// "test" operation. If the array has been concurrently modified (e.g. by another
// controller removing a different finalizer), the test fails atomically and the
// caller should retry with a fresh read.
// This prevents the race condition where a merge-patch based on a stale cache
// inadvertently re-adds finalizers removed by other controllers.
// See https://github.com/kubernetes-csi/external-provisioner/issues/1217.
func removeFinalizerPatch(existingFinalizers []string, finalizerName string) ([]byte, error) {
	type patchOp struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value,omitempty"`
	}

	idx := -1
	for i, f := range existingFinalizers {
		if f == finalizerName {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("finalizer %q not found in list", finalizerName)
	}

	path := fmt.Sprintf("/metadata/finalizers/%d", idx)
	ops := []patchOp{
		{Op: "test", Path: path, Value: finalizerName},
		{Op: "remove", Path: path},
	}
	return json.Marshal(ops)
}
