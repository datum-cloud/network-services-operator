package downstreamclient

import (
	"crypto/md5"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ ResourceStrategy = &sameNamespaceResourceStrategy{}

func NewSameNamespaceResourceStrategy(client client.Client) ResourceStrategy {
	return &sameNamespaceResourceStrategy{
		client: client,
	}
}

type sameNamespaceResourceStrategy struct {
	client client.Client
}

func (c *sameNamespaceResourceStrategy) GetClient() client.Client {
	return c.client
}

// ObjectMetaFromObject returns a name derived from the input object's name, where
// the value is the first 188 characters of the input object's name, suffixed by
// the sha256 hash of the full input object's name.
func (c *sameNamespaceResourceStrategy) ObjectMetaFromObject(obj metav1.Object) metav1.ObjectMeta {
	upstreamName := obj.GetName()

	// MD5 produces 32 hex characters
	hash := md5.Sum([]byte(upstreamName))

	// Reserve 33 chars for hash and hyphen (32 for MD5 + 1 for hyphen)
	// This leaves 30 chars for the prefix
	maxPrefixLen := 30
	if len(upstreamName) > maxPrefixLen {
		upstreamName = upstreamName[0:maxPrefixLen]
	}

	return metav1.ObjectMeta{
		Namespace: obj.GetNamespace(),
		Name:      fmt.Sprintf("%s-%x", upstreamName, hash),
	}
}
