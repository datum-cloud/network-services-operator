// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	cmacmev1 "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"go.datum.net/network-services-operator/internal/config"
)

func TestChallengeReconciler(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, cmacmev1.AddToScheme(testScheme))

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cert-manager",
			UID:  uuid.NewUUID(),
		},
	}

	tests := []struct {
		name          string
		challenge     *cmacmev1.Challenge
		config        config.NetworkServicesOperator
		expectDeleted bool
		expectError   bool
		assertFn      func(t *testing.T, challenge *cmacmev1.Challenge, result ctrl.Result, err error)
	}{
		{
			name: "errored challenge with ClusterIssuer in map is deleted",
			challenge: &cmacmev1.Challenge{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamNamespace.Name,
					Name:      "test-challenge",
					UID:       uuid.NewUUID(),
				},
				Spec: cmacmev1.ChallengeSpec{
					DNSName: "example.com",
					IssuerRef: cmmeta.ObjectReference{
						Kind: "ClusterIssuer",
						Name: "letsencrypt-prod",
					},
				},
				Status: cmacmev1.ChallengeStatus{
					State:  cmacmev1.Errored,
					Reason: "ACME server returned error: rate limit exceeded",
				},
			},
			config: config.NetworkServicesOperator{
				Gateway: config.GatewayConfig{
					ClusterIssuerMap: map[string]string{
						"letsencrypt": "letsencrypt-prod",
					},
				},
			},
			expectDeleted: true,
		},
		{
			name: "errored challenge with Issuer in per-gateway mode is deleted",
			challenge: &cmacmev1.Challenge{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamNamespace.Name,
					Name:      "test-challenge-issuer",
					UID:       uuid.NewUUID(),
				},
				Spec: cmacmev1.ChallengeSpec{
					DNSName: "example.com",
					IssuerRef: cmmeta.ObjectReference{
						Kind: "Issuer",
						Name: "my-gateway-issuer",
					},
				},
				Status: cmacmev1.ChallengeStatus{
					State:  cmacmev1.Errored,
					Reason: "DNS challenge verification failed",
				},
			},
			config: config.NetworkServicesOperator{
				Gateway: config.GatewayConfig{
					PerGatewayCertificateIssuer: true,
				},
			},
			expectDeleted: true,
		},
		{
			name: "errored challenge with non-gateway ClusterIssuer is not deleted",
			challenge: &cmacmev1.Challenge{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamNamespace.Name,
					Name:      "test-challenge-unrelated",
					UID:       uuid.NewUUID(),
				},
				Spec: cmacmev1.ChallengeSpec{
					DNSName: "example.com",
					IssuerRef: cmmeta.ObjectReference{
						Kind: "ClusterIssuer",
						Name: "some-other-issuer",
					},
				},
				Status: cmacmev1.ChallengeStatus{
					State:  cmacmev1.Errored,
					Reason: "ACME server returned error",
				},
			},
			config: config.NetworkServicesOperator{
				Gateway: config.GatewayConfig{
					ClusterIssuerMap: map[string]string{
						"letsencrypt": "letsencrypt-prod",
					},
				},
			},
			expectDeleted: false,
		},
		{
			name: "pending challenge is not deleted",
			challenge: &cmacmev1.Challenge{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamNamespace.Name,
					Name:      "test-challenge-pending",
					UID:       uuid.NewUUID(),
				},
				Spec: cmacmev1.ChallengeSpec{
					DNSName: "example.com",
					IssuerRef: cmmeta.ObjectReference{
						Kind: "ClusterIssuer",
						Name: "letsencrypt-prod",
					},
				},
				Status: cmacmev1.ChallengeStatus{
					State: cmacmev1.Pending,
				},
			},
			config: config.NetworkServicesOperator{
				Gateway: config.GatewayConfig{
					ClusterIssuerMap: map[string]string{
						"letsencrypt": "letsencrypt-prod",
					},
				},
			},
			expectDeleted: false,
		},
		{
			name: "valid challenge is not deleted",
			challenge: &cmacmev1.Challenge{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamNamespace.Name,
					Name:      "test-challenge-valid",
					UID:       uuid.NewUUID(),
				},
				Spec: cmacmev1.ChallengeSpec{
					DNSName: "example.com",
					IssuerRef: cmmeta.ObjectReference{
						Kind: "ClusterIssuer",
						Name: "letsencrypt-prod",
					},
				},
				Status: cmacmev1.ChallengeStatus{
					State: cmacmev1.Valid,
				},
			},
			config: config.NetworkServicesOperator{
				Gateway: config.GatewayConfig{
					ClusterIssuerMap: map[string]string{
						"letsencrypt": "letsencrypt-prod",
					},
				},
			},
			expectDeleted: false,
		},
		{
			name: "challenge not found returns no error",
			challenge: &cmacmev1.Challenge{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamNamespace.Name,
					Name:      "nonexistent-challenge",
				},
			},
			config: config.NetworkServicesOperator{
				Gateway: config.GatewayConfig{
					ClusterIssuerMap: map[string]string{
						"letsencrypt": "letsencrypt-prod",
					},
				},
			},
			expectDeleted: false,
			assertFn: func(t *testing.T, challenge *cmacmev1.Challenge, result ctrl.Result, err error) {
				assert.NoError(t, err)
				assert.Zero(t, result.RequeueAfter)
			},
		},
		{
			name: "errored challenge with empty kind (defaults to ClusterIssuer) in map is deleted",
			challenge: &cmacmev1.Challenge{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamNamespace.Name,
					Name:      "test-challenge-empty-kind",
					UID:       uuid.NewUUID(),
				},
				Spec: cmacmev1.ChallengeSpec{
					DNSName: "example.com",
					IssuerRef: cmmeta.ObjectReference{
						Kind: "", // Empty kind defaults to ClusterIssuer
						Name: "letsencrypt-prod",
					},
				},
				Status: cmacmev1.ChallengeStatus{
					State:  cmacmev1.Errored,
					Reason: "Challenge failed",
				},
			},
			config: config.NetworkServicesOperator{
				Gateway: config.GatewayConfig{
					ClusterIssuerMap: map[string]string{
						"letsencrypt": "letsencrypt-prod",
					},
				},
			},
			expectDeleted: true,
		},
		{
			name: "errored challenge with Issuer but not in per-gateway mode is not deleted",
			challenge: &cmacmev1.Challenge{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamNamespace.Name,
					Name:      "test-challenge-issuer-no-gateway",
					UID:       uuid.NewUUID(),
				},
				Spec: cmacmev1.ChallengeSpec{
					DNSName: "example.com",
					IssuerRef: cmmeta.ObjectReference{
						Kind: "Issuer",
						Name: "my-issuer",
					},
				},
				Status: cmacmev1.ChallengeStatus{
					State:  cmacmev1.Errored,
					Reason: "Challenge failed",
				},
			},
			config: config.NetworkServicesOperator{
				Gateway: config.GatewayConfig{
					PerGatewayCertificateIssuer: false,
					ClusterIssuerMap: map[string]string{
						"letsencrypt": "letsencrypt-prod",
					},
				},
			},
			expectDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Build the fake client with or without the challenge
			var objects []client.Object
			objects = append(objects, upstreamNamespace)
			challengeExists := tt.challenge.UID != ""
			if challengeExists {
				objects = append(objects, tt.challenge)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(objects...).
				Build()

			reconciler := &ChallengeReconciler{
				DownstreamCluster: &fakeCluster{cl: fakeClient},
				Config:            tt.config,
			}

			result, err := reconciler.Reconcile(
				ctx,
				reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(tt.challenge),
				},
			)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Check if the challenge was deleted
			var foundChallenge cmacmev1.Challenge
			getErr := fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.challenge), &foundChallenge)

			if tt.expectDeleted {
				assert.True(t, apierrors.IsNotFound(getErr), "expected challenge to be deleted")
			} else if challengeExists {
				assert.NoError(t, getErr, "expected challenge to still exist")
			}

			if tt.assertFn != nil {
				tt.assertFn(t, tt.challenge, result, err)
			}
		})
	}
}

func TestIsGatewayRelatedIssuer(t *testing.T) {
	tests := []struct {
		name     string
		ref      cmmeta.ObjectReference
		config   config.GatewayConfig
		expected bool
	}{
		{
			name: "ClusterIssuer in map returns true",
			ref: cmmeta.ObjectReference{
				Kind: "ClusterIssuer",
				Name: "letsencrypt-prod",
			},
			config: config.GatewayConfig{
				ClusterIssuerMap: map[string]string{
					"letsencrypt": "letsencrypt-prod",
				},
			},
			expected: true,
		},
		{
			name: "ClusterIssuer not in map returns false",
			ref: cmmeta.ObjectReference{
				Kind: "ClusterIssuer",
				Name: "other-issuer",
			},
			config: config.GatewayConfig{
				ClusterIssuerMap: map[string]string{
					"letsencrypt": "letsencrypt-prod",
				},
			},
			expected: false,
		},
		{
			name: "empty kind (defaults to ClusterIssuer) in map returns true",
			ref: cmmeta.ObjectReference{
				Kind: "",
				Name: "letsencrypt-prod",
			},
			config: config.GatewayConfig{
				ClusterIssuerMap: map[string]string{
					"letsencrypt": "letsencrypt-prod",
				},
			},
			expected: true,
		},
		{
			name: "Issuer in per-gateway mode returns true",
			ref: cmmeta.ObjectReference{
				Kind: "Issuer",
				Name: "gateway-issuer",
			},
			config: config.GatewayConfig{
				PerGatewayCertificateIssuer: true,
			},
			expected: true,
		},
		{
			name: "Issuer not in per-gateway mode returns false",
			ref: cmmeta.ObjectReference{
				Kind: "Issuer",
				Name: "gateway-issuer",
			},
			config: config.GatewayConfig{
				PerGatewayCertificateIssuer: false,
			},
			expected: false,
		},
		{
			name: "empty map returns false",
			ref: cmmeta.ObjectReference{
				Kind: "ClusterIssuer",
				Name: "any-issuer",
			},
			config:   config.GatewayConfig{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ChallengeReconciler{
				Config: config.NetworkServicesOperator{
					Gateway: tt.config,
				},
			}

			result := reconciler.isGatewayRelatedIssuer(tt.ref)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldDeleteErroredChallenges(t *testing.T) {
	tests := []struct {
		name     string
		config   config.GatewayConfig
		expected bool
	}{
		{
			name:     "nil DeleteErroredChallenges defaults to true",
			config:   config.GatewayConfig{},
			expected: true,
		},
		{
			name: "true DeleteErroredChallenges returns true",
			config: config.GatewayConfig{
				DeleteErroredChallenges: ptr.To(true),
			},
			expected: true,
		},
		{
			name: "false DeleteErroredChallenges returns false",
			config: config.GatewayConfig{
				DeleteErroredChallenges: ptr.To(false),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.ShouldDeleteErroredChallenges()
			assert.Equal(t, tt.expected, result)
		})
	}
}
