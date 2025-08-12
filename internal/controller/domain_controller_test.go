package controller

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

func TestDomainVerification(t *testing.T) {
	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(testScheme))

	operatorConfig := config.NetworkServicesOperator{
		DomainVerification: config.DomainVerificationConfig{
			DNSVerificationRecordPrefix: "_dnsverify",
			HTTPVerificationTokenPath:   "dnsverify",
			RetryIntervals: []config.RetryInterval{
				{
					Interval: metav1.Duration{
						Duration: 5 * time.Second,
					},
				},
			},
		},
	}

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  uuid.NewUUID(),
		},
	}

	tests := []struct {
		name      string
		domain    *networkingv1alpha.Domain
		timeNow   func() time.Time
		httpGet   func(ctx context.Context, url string) ([]byte, *http.Response, error)
		lookupTXT func(ctx context.Context, name string) ([]string, error)
		assert    func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result)
	}{
		{
			name:   "verification details added to status",
			domain: newDomain(upstreamNamespace.Name, "test"),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, _ ctrl.Result) {
				if assert.NotNil(t, domain.Status.Verification, "domain verification details missing") {
					assert.Equal(
						t,
						fmt.Sprintf("%s.%s", operatorConfig.DomainVerification.DNSVerificationRecordPrefix, domain.Spec.DomainName),
						domain.Status.Verification.DNSRecord.Name,
					)
					assert.NotEmpty(t, domain.Status.Verification.DNSRecord.Content)

					assert.Equal(
						t,
						fmt.Sprintf("http://%s/%s/%s", domain.Spec.DomainName, operatorConfig.DomainVerification.HTTPVerificationTokenPath, domain.UID),
						domain.Status.Verification.HTTPToken.URL,
					)
					assert.NotEmpty(t, domain.Status.Verification.HTTPToken.Body)
				}
			},
		},
		{
			name: "requeue for next verification attempt",
			timeNow: func() time.Time {
				t, _ := time.Parse(time.DateTime, "2025-08-04 17:26:00")
				return t
			},
			domain: newDomain(upstreamNamespace.Name, "test", func(domain *networkingv1alpha.Domain) {
				t, _ := time.Parse(time.DateTime, "2025-08-04 17:26:05")
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					NextVerificationAttempt: metav1.Time{Time: t},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				assert.Equal(t, 5*time.Second, result.RequeueAfter)
			},
		},
		{
			name: "dns txt record not found",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, &net.DNSError{IsNotFound: true}
			},
			domain: newDomain(upstreamNamespace.Name, "dns-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					DNSRecord: networkingv1alpha.DNSVerificationRecord{
						Name:    "test",
						Type:    "TXT",
						Content: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
				if assert.NotNil(t, condition, "VerifiedDNS condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonVerificationRecordNotFound, condition.Reason)
				}
			},
		},
		{
			name: "dns temporary error",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, &net.DNSError{IsTemporary: true}
			},
			domain: newDomain(upstreamNamespace.Name, "dns-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					DNSRecord: networkingv1alpha.DNSVerificationRecord{
						Name:    "test",
						Type:    "TXT",
						Content: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
				if assert.NotNil(t, condition, "VerifiedDNS condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonPendingVerification, condition.Reason)
				}
			},
		},
		{
			name: "dns timeout",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, &net.DNSError{IsTemporary: true}
			},
			domain: newDomain(upstreamNamespace.Name, "dns-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					DNSRecord: networkingv1alpha.DNSVerificationRecord{
						Name:    "test",
						Type:    "TXT",
						Content: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
				if assert.NotNil(t, condition, "VerifiedDNS condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonPendingVerification, condition.Reason)
				}
			},
		},
		{
			name: "dns lookup error",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, &net.DNSError{}
			},
			domain: newDomain(upstreamNamespace.Name, "dns-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					DNSRecord: networkingv1alpha.DNSVerificationRecord{
						Name:    "test",
						Type:    "TXT",
						Content: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
				if assert.NotNil(t, condition, "VerifiedDNS condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonVerificationInternalError, condition.Reason)
				}
			},
		},
		{
			name: "dns internal error",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, fmt.Errorf("unexpected error")
			},
			domain: newDomain(upstreamNamespace.Name, "dns-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					DNSRecord: networkingv1alpha.DNSVerificationRecord{
						Name:    "test",
						Type:    "TXT",
						Content: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
				if assert.NotNil(t, condition, "VerifiedDNS condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonVerificationInternalError, condition.Reason)
				}
			},
		},
		{
			name: "dns record content mismatch",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{"not-expected"}, nil
			},
			domain: newDomain(upstreamNamespace.Name, "dns-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					DNSRecord: networkingv1alpha.DNSVerificationRecord{
						Name:    "test",
						Type:    "TXT",
						Content: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS)
				if assert.NotNil(t, condition, "VerifiedDNS condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonVerificationRecordContentMismatch, condition.Reason)
				}
			},
		},
		{
			name: "dns record verification successful",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{"test"}, nil
			},
			domain: newDomain(upstreamNamespace.Name, "dns-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					DNSRecord: networkingv1alpha.DNSVerificationRecord{
						Name:    "test",
						Type:    "TXT",
						Content: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				assert.True(t, apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerified))
				assert.Nil(t, apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS), "expected VerifiedDNS condition to not be present")
				assert.Nil(t, apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedHTTP), "expected VerifiedHTTP condition to not be present")
			},
		},
		{
			name: "http token not found",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, &net.DNSError{IsNotFound: true}
			},
			httpGet: func(ctx context.Context, url string) ([]byte, *http.Response, error) {
				return nil, &http.Response{StatusCode: http.StatusNotFound}, nil
			},
			domain: newDomain(upstreamNamespace.Name, "http-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					HTTPToken: networkingv1alpha.HTTPVerificationToken{
						URL:  "test",
						Body: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedHTTP)
				if assert.NotNil(t, condition, "VerifiedHTTP condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonVerificationRecordNotFound, condition.Reason)
				}
			},
		},
		{
			name: "unexpected http response",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, &net.DNSError{IsNotFound: true}
			},
			httpGet: func(ctx context.Context, url string) ([]byte, *http.Response, error) {
				return nil, &http.Response{StatusCode: http.StatusAccepted}, nil
			},
			domain: newDomain(upstreamNamespace.Name, "http-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					HTTPToken: networkingv1alpha.HTTPVerificationToken{
						URL:  "test",
						Body: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedHTTP)
				if assert.NotNil(t, condition, "VerifiedHTTP condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonVerificationUnexpectedResponse, condition.Reason)
				}
			},
		},
		{
			name: "http token content mismatch",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, &net.DNSError{IsNotFound: true}
			},
			httpGet: func(ctx context.Context, url string) ([]byte, *http.Response, error) {
				return []byte("not-expected"), &http.Response{StatusCode: http.StatusOK}, nil
			},
			domain: newDomain(upstreamNamespace.Name, "http-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					HTTPToken: networkingv1alpha.HTTPVerificationToken{
						URL:  "test",
						Body: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				condition := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedHTTP)
				if assert.NotNil(t, condition, "VerifiedHTTP condition not found") {
					assert.Equal(t, networkingv1alpha.DomainReasonVerificationRecordContentMismatch, condition.Reason)
				}
			},
		},
		{
			name: "http token content mismatch",
			lookupTXT: func(ctx context.Context, name string) ([]string, error) {
				return []string{}, &net.DNSError{IsNotFound: true}
			},
			httpGet: func(ctx context.Context, url string) ([]byte, *http.Response, error) {
				return []byte("test"), &http.Response{StatusCode: http.StatusOK}, nil
			},
			domain: newDomain(upstreamNamespace.Name, "http-verify", func(domain *networkingv1alpha.Domain) {
				domain.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
					HTTPToken: networkingv1alpha.HTTPVerificationToken{
						URL:  "test",
						Body: "test",
					},
				}
			}),
			assert: func(t *testing.T, domain *networkingv1alpha.Domain, result ctrl.Result) {
				assert.True(t, apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerified))
				assert.Nil(t, apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNS), "expected VerifiedDNS condition to not be present")
				assert.Nil(t, apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedHTTP), "expected VerifiedHTTP condition to not be present")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			if tt.timeNow == nil {
				tt.timeNow = time.Now
			}

			if tt.httpGet == nil {
				tt.httpGet = func(ctx context.Context, url string) ([]byte, *http.Response, error) {
					return nil, nil, fmt.Errorf("not implemented")
				}
			}

			if tt.lookupTXT == nil {
				tt.lookupTXT = func(ctx context.Context, name string) ([]string, error) {
					return []string{}, fmt.Errorf("not implemented")
				}
			}

			fakeUpstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.domain, upstreamNamespace).
				WithStatusSubresource(tt.domain).
				Build()

			ctx := context.Background()

			mgr := &fakeMockManager{cl: fakeUpstreamClient}

			reconciler := &DomainReconciler{
				mgr:    mgr,
				Config: operatorConfig,

				timeNow:   tt.timeNow,
				httpGet:   tt.httpGet,
				lookupTXT: tt.lookupTXT,
			}

			result, err := reconciler.Reconcile(
				ctx,
				mcreconcile.Request{
					ClusterName: "test",
					Request: reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(tt.domain),
					},
				},
			)

			if assert.NoError(t, err, "unexpected error during reconcile") {
				updatedDomain := &networkingv1alpha.Domain{}

				assert.NoError(t, fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(tt.domain), updatedDomain))

				tt.assert(t, updatedDomain, result)
			}
		})
	}
}

func newDomain(namespace, name string, opts ...func(*networkingv1alpha.Domain)) *networkingv1alpha.Domain {
	domain := &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: networkingv1alpha.DomainSpec{
			DomainName: "example.com",
		},
	}

	for _, opt := range opts {
		opt(domain)
	}

	return domain
}
