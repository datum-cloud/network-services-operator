package controller

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
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

// TODO(jreese): I can't seem to get these indexers to function on the downstream
// cluster. From tracing the code, the indexers get invoked, but I still get
// an error that the index does not exist when trying to list resources.
func AddCertManagerIndexers(ctx context.Context, cl cluster.Cluster) error {
	return errors.Join(
		addCertManagerOrderIndexers(ctx, cl),
		addCertManagerChallengeIndexers(ctx, cl),
	)
}

const certManagerCertificateNameAnnotation = "cert-manager.io/certificate-name"

func addCertManagerOrderIndexers(ctx context.Context, cl cluster.Cluster) error {
	if err := cl.GetFieldIndexer().IndexField(ctx, newUnstructuredForGVK(certificateGVK), certManagerCertificateNameAnnotation, certManagerOrderCertificateNameIndexFunc); err != nil {
		return fmt.Errorf("failed to add cert-manager order certificate name indexer %q: %w", certManagerCertificateNameAnnotation, err)
	}

	return nil
}

func certManagerOrderCertificateNameIndexFunc(o client.Object) []string {
	annotations := o.GetAnnotations()
	if certName, ok := annotations[certManagerCertificateNameAnnotation]; ok {
		return []string{certName}
	}
	return nil
}

// Index Challenges by their owner name as `kind:name`
const certManagerChallengeOwnerIndex = "certManagerChallengeOwnerIndex"

func addCertManagerChallengeIndexers(ctx context.Context, cl cluster.Cluster) error {
	if err := cl.GetFieldIndexer().IndexField(ctx, newUnstructuredForGVK(challengeGVK), certManagerChallengeOwnerIndex, certManagerChallengeOwnerIndexFunc); err != nil {
		return fmt.Errorf("failed to add cert-manager challenge owner indexer %q: %w", certManagerChallengeOwnerIndex, err)
	}

	return nil
}

func certManagerChallengeOwnerIndexFunc(o client.Object) []string {
	owners := o.GetOwnerReferences()
	var result []string
	for _, owner := range owners {
		result = append(result, ownerIndexValue(owner.Kind, owner.Name))
	}
	return result
}

func ownerIndexValue(kind, name string) string {
	return fmt.Sprintf("%s:%s", kind, name)
}
