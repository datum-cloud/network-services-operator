// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

var coordinationLeaseGroupKind = schema.GroupKind{
	Group: coordinationv1.GroupName,
	Kind:  "Lease",
}

func onlyClustersServingLeases() mcbuilder.WatchesOption {
	return mcbuilder.WithClusterFilter(func(_ multicluster.ClusterName, cl cluster.Cluster) bool {
		return clusterServesLeases(cl.GetRESTMapper())
	})
}

func clusterServesLeases(mapper meta.RESTMapper) bool {
	if mapper == nil {
		return false
	}
	_, err := mapper.RESTMapping(coordinationLeaseGroupKind, coordinationv1.SchemeGroupVersion.Version)
	return err == nil
}
