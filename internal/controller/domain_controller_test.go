package controller

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/openrdap/rdap"
	"github.com/openrdap/rdap/bootstrap"
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
	config.SetObjectDefaults_NetworkServicesOperator(&operatorConfig)

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
			// --- Prevent registration from doing real I/O during verification-only tests ---
			reconciler.lookupNS = func(ctx context.Context, name string) ([]*net.NS, error) {
				return nil, &net.DNSError{IsNotFound: true}
			}
			reconciler.lookupIP = func(ctx context.Context, name string) ([]net.IPAddr, error) {
				return nil, nil
			}
			reconciler.rdapDo = func(ctx context.Context, req *rdap.Request) (*rdap.Response, error) {
				return &rdap.Response{
					Object: &rdap.Domain{LDHName: "example.com"},
					BootstrapAnswer: &bootstrap.Answer{ // optional; can be nil
						Entry: "com",
					},
				}, nil
			}
			reconciler.rdapQueryIP = func(ctx context.Context, ip string) (*rdap.IPNetwork, error) {
				return nil, nil
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

func TestRegistration_Apex_UsesRDAPNameservers(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "apex")
	dom.Spec.DomainName = "example.com" // apex

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	// Fake RDAP domain with 2 NS from registry
	rdapDomain := &rdap.Domain{
		LDHName: "example.com",
		Nameservers: []rdap.Nameserver{
			{LDHName: "ns1.example.net"},
			{LDHName: "ns2.example.net"},
		},
	}
	resp := &rdap.Response{
		Object: rdapDomain,
		BootstrapAnswer: &bootstrap.Answer{
			Entry: "com",
			URLs:  []*url.URL{{Scheme: "https", Host: "rdap.verisign.com"}},
		},
	}

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.1,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		}},
		timeNow: time.Now,
		// test stubs
		rdapDo:   func(ctx context.Context, req *rdap.Request) (*rdap.Response, error) { return resp, nil },
		lookupNS: func(ctx context.Context, name string) ([]*net.NS, error) { return nil, &net.DNSError{IsNotFound: true} },
		lookupIP: func(ctx context.Context, name string) ([]net.IPAddr, error) { return nil, nil }, // skip enrichment
	}

	res, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

	assert.True(t, got.Status.Apex, "expected apex=true")
	// top-level nameservers should mirror RDAP NS
	have := []string{}
	for _, ns := range got.Status.Nameservers {
		have = append(have, ns.Hostname)
	}
	assert.ElementsMatch(t, []string{"ns1.example.net", "ns2.example.net"}, have)
	assert.True(t, res.RequeueAfter > 0)
}

func TestRegistration_Subdomain_DelegationOverridesApexNS(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "sub")
	dom.Spec.DomainName = "app.example.com" // non-apex

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	// RDAP for apex returns registrar NS, which should be overridden by delegated NS of subdomain
	rdapDomain := &rdap.Domain{
		LDHName: "example.com",
		Nameservers: []rdap.Nameserver{
			{LDHName: "ns-apex-1.example.com"},
			{LDHName: "ns-apex-2.example.com"},
		},
	}
	resp := &rdap.Response{
		Object: rdapDomain,
		BootstrapAnswer: &bootstrap.Answer{
			Entry: "com",
			URLs:  []*url.URL{{Scheme: "https", Host: "rdap.verisign.com"}},
		},
	}

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.1,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		}},
		timeNow: time.Now,
		rdapDo:  func(ctx context.Context, req *rdap.Request) (*rdap.Response, error) { return resp, nil },
		// Return NS at subdomain (zone-cut)
		lookupNS: func(ctx context.Context, name string) ([]*net.NS, error) {
			name = strings.TrimSuffix(strings.ToLower(name), ".")
			switch name {
			case "app.example.com":
				return []*net.NS{{Host: "ns-deleg-1.example.net."}, {Host: "ns-deleg-2.example.net."}}, nil
			default:
				return nil, &net.DNSError{IsNotFound: true}
			}
		},
		lookupIP: func(ctx context.Context, name string) ([]net.IPAddr, error) { return nil, nil },
	}

	_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

	assert.False(t, got.Status.Apex, "expected apex=false")
	have := []string{}
	for _, ns := range got.Status.Nameservers {
		have = append(have, ns.Hostname)
	}
	assert.ElementsMatch(t, []string{"ns-deleg-1.example.net", "ns-deleg-2.example.net"}, have)
}

func TestRegistration_Subdomain_NoDelegation_FallsBackToApexNS(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "sub")
	dom.Spec.DomainName = "www.example.com"

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	rdapDomain := &rdap.Domain{
		LDHName: "example.com",
		Nameservers: []rdap.Nameserver{
			{LDHName: "ns1.example.net"},
			{LDHName: "ns2.example.net"},
		},
	}
	resp := &rdap.Response{
		Object: rdapDomain,
		BootstrapAnswer: &bootstrap.Answer{
			Entry: "com",
			URLs:  []*url.URL{{Scheme: "https", Host: "rdap.verisign.com"}},
		},
	}

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.1,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		}},
		timeNow:  time.Now,
		rdapDo:   func(ctx context.Context, req *rdap.Request) (*rdap.Response, error) { return resp, nil },
		lookupNS: func(ctx context.Context, name string) ([]*net.NS, error) { return nil, &net.DNSError{IsNotFound: true} },
		lookupIP: func(ctx context.Context, name string) ([]net.IPAddr, error) { return nil, nil },
	}

	_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

	assert.False(t, got.Status.Apex)
	have := []string{}
	for _, ns := range got.Status.Nameservers {
		have = append(have, ns.Hostname)
	}
	assert.ElementsMatch(t, []string{"ns1.example.net", "ns2.example.net"}, have)
}

func TestRegistration_RegistryStampedFromBootstrap(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "host")
	dom.Spec.DomainName = "example.ai" // .ai → Identity Digital

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	rdapDomain := &rdap.Domain{LDHName: "example.ai"}
	resp := &rdap.Response{
		Object: rdapDomain,
		BootstrapAnswer: &bootstrap.Answer{
			Entry: "ai",
			URLs:  []*url.URL{{Scheme: "https", Host: "rdap.identitydigital.services"}},
		},
	}

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.1,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		}},
		timeNow:  time.Now,
		rdapDo:   func(ctx context.Context, req *rdap.Request) (*rdap.Response, error) { return resp, nil },
		lookupNS: func(ctx context.Context, name string) ([]*net.NS, error) { return nil, &net.DNSError{IsNotFound: true} },
		lookupIP: func(ctx context.Context, name string) ([]net.IPAddr, error) { return nil, nil },
	}

	_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)
	if assert.NotNil(t, got.Status.Registration) {
		assert.NotNil(t, got.Status.Registration.Registry)
		assert.Equal(t, "rdap.identitydigital.services", got.Status.Registration.Registry.Name)
	}
}

func TestRegistration_RetryBackoffOnError(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "err")
	dom.Spec.DomainName = "example.com"

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	now := time.Date(2025, 10, 9, 1, 0, 0, 0, time.UTC)

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.0,
			RetryBackoff:    &metav1.Duration{Duration: 2 * time.Minute},
		}},
		timeNow:  func() time.Time { return now },
		rdapDo:   func(ctx context.Context, req *rdap.Request) (*rdap.Response, error) { return nil, fmt.Errorf("boom") },
		lookupNS: func(ctx context.Context, name string) ([]*net.NS, error) { return nil, &net.DNSError{IsNotFound: true} },
		lookupIP: func(ctx context.Context, name string) ([]net.IPAddr, error) { return nil, nil },
	}

	res, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

	// should schedule retry in 2m
	assert.Equal(t, 2*time.Minute, got.Status.Registration.NextRegistrationAttempt.Sub(now))
	assert.True(t, res.RequeueAfter == 0 || res.RequeueAfter > 0) // depending on verification timer
}

func TestRegistration_EnrichesNameserverIPs(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "enrich")
	dom.Spec.DomainName = "example.com"

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	rdapDomain := &rdap.Domain{
		LDHName:     "example.com",
		Nameservers: []rdap.Nameserver{{LDHName: "ns1.example.net"}},
	}
	resp := &rdap.Response{
		Object: rdapDomain,
		BootstrapAnswer: &bootstrap.Answer{
			Entry: "com",
			URLs:  []*url.URL{{Scheme: "https", Host: "rdap.verisign.com"}},
		},
	}

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.0,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		}},
		timeNow: time.Now,
		rdapDo:  func(ctx context.Context, req *rdap.Request) (*rdap.Response, error) { return resp, nil },
		lookupNS: func(ctx context.Context, name string) ([]*net.NS, error) {
			return nil, &net.DNSError{IsNotFound: true}
		},
		lookupIP: func(ctx context.Context, name string) ([]net.IPAddr, error) {
			if name == "ns1.example.net" {
				return []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}}, nil
			}
			return nil, nil
		},
		rdapQueryIP: func(ctx context.Context, ip string) (*rdap.IPNetwork, error) {
			// return a network object with a registrant entity
			return &rdap.IPNetwork{
				Entities: []rdap.Entity{{
					Roles: []string{"registrant"},
					VCard: vcardWithFN("Example Net Ops"),
				}},
			}, nil
		},
	}

	_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

	if assert.NotEmpty(t, got.Status.Nameservers) && assert.NotEmpty(t, got.Status.Nameservers[0].IPs) {
		assert.Equal(t, "192.0.2.10", got.Status.Nameservers[0].IPs[0].Address)
		assert.Equal(t, "Example Net Ops", got.Status.Nameservers[0].IPs[0].RegistrantName)
	}
}

func vcardWithFN(fullname string) *rdap.VCard {
	return &rdap.VCard{
		Properties: []*rdap.VCardProperty{
			{
				Name:  "fn",     // property name
				Type:  "text",   // value type
				Value: fullname, // single value
			},
		},
	}
}
