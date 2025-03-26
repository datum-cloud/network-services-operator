package downstreamclient

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ResourceStrategy interface {
	GetClient() client.Client

	// ObjectMetaFromObject returns an ObjectMeta struct with Namespace and
	// Name fields populated for the downstream resource.
	ObjectMetaFromObject(metav1.Object) metav1.ObjectMeta
}
