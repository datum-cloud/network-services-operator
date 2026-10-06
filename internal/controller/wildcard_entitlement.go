// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

const (
	WildcardHostnamesResourceType = "networking.datumapis.com/wildcard-hostnames"

	wildcardEntitlementBucketNamespace = "milo-system"
	wildcardEntitlementConsumerKind    = "Project"

	wildcardEntitlementRecheck = time.Minute

	certificateServiceReasonWildcardNotEntitled = "WildcardNotEntitled"
)

func wildcardNotEntitledMessage(hostname string) string {
	return fmt.Sprintf("Wildcard hostnames are not enabled for this project, so no TLS certificate is issued for %s. Contact Datum to enable them.", hostname)
}

type WildcardEntitlementChecker interface {
	WildcardEntitled(ctx context.Context, projectName string) (bool, error)
}

type bucketWildcardEntitlements struct {
	reader client.Reader
}

func NewWildcardEntitlementChecker(reader client.Reader) WildcardEntitlementChecker {
	return &bucketWildcardEntitlements{reader: reader}
}

func allowanceBucketName(resourceType, consumerKind, consumerName string) string {
	return fmt.Sprintf("bucket-%x", sha256.Sum256([]byte(resourceType+consumerKind+consumerName)))
}

func (b *bucketWildcardEntitlements) WildcardEntitled(ctx context.Context, projectName string) (bool, error) {
	var bucket quotav1alpha1.AllowanceBucket
	key := client.ObjectKey{
		Namespace: wildcardEntitlementBucketNamespace,
		Name:      allowanceBucketName(WildcardHostnamesResourceType, wildcardEntitlementConsumerKind, projectName),
	}
	if err := b.reader.Get(ctx, key, &bucket); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read AllowanceBucket %s: %w", key, err)
	}
	return bucket.Status.Available > 0, nil
}

// wildcardEntitled reports whether the project may have wildcard hostnames. A
// read error is returned rather than folded into "no": only a definite denial
// may withdraw a certificate that is already serving.
func (r *GatewayReconciler) wildcardEntitled(ctx context.Context, projectName string) (bool, error) {
	if r.WildcardEntitlements == nil {
		return false, nil
	}
	return r.WildcardEntitlements.WildcardEntitled(ctx, projectName)
}
