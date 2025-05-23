package controller

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	networkContextControllerNetworkUIDIndex = "networkContextControllerNetworkUIDIndex"
)

func AddIndexers(ctx context.Context, mgr mcmanager.Manager) error {
	return errors.Join(
		addNetworkContextControllerIndexers(ctx, mgr),
	)
}

func addNetworkContextControllerIndexers(ctx context.Context, mgr mcmanager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &networkingv1alpha.NetworkContext{}, networkContextControllerNetworkUIDIndex, networkContextControllerNetworkUIDIndexFunc); err != nil {
		return fmt.Errorf("failed to add network context controller indexer %q: %w", networkContextControllerNetworkUIDIndex, err)
	}

	return nil
}

func networkContextControllerNetworkUIDIndexFunc(o client.Object) []string {

	if networkRef := metav1.GetControllerOf(o); networkRef != nil {
		return []string{
			string(networkRef.UID),
		}
	}

	return nil
}
