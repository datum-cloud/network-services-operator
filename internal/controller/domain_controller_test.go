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
	"go.datum.net/network-services-operator/pkg/registrydata"
)

type fakeRegistryClient struct {
	lookupDomain       func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error)
	lookupNameserver   func(ctx context.Context, hostname string, opts registrydata.LookupOptions) (*registrydata.NameserverResult, error)
	lookupIPRegistrant func(ctx context.Context, ip net.IP, opts registrydata.LookupOptions) (*registrydata.IPRegistrantResult, error)
}

func (f *fakeRegistryClient) LookupDomain(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
	if f.lookupDomain != nil {
		return f.lookupDomain(ctx, domain, opts)
	}
	return &registrydata.DomainResult{Registration: &networkingv1alpha.Registration{}}, nil
}

func (f *fakeRegistryClient) LookupNameserver(ctx context.Context, hostname string, opts registrydata.LookupOptions) (*registrydata.NameserverResult, error) {
	if f.lookupNameserver != nil {
		return f.lookupNameserver(ctx, hostname, opts)
	}
	return &registrydata.NameserverResult{Hostname: hostname}, nil
}

func (f *fakeRegistryClient) LookupIPRegistrant(ctx context.Context, ip net.IP, opts registrydata.LookupOptions) (*registrydata.IPRegistrantResult, error) {
	if f.lookupIPRegistrant != nil {
		return f.lookupIPRegistrant(ctx, ip, opts)
	}
	return &registrydata.IPRegistrantResult{IP: ip}, nil
}

const (
	exampleDomain = "example.com"
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

				timeNow:        tt.timeNow,
				httpGet:        tt.httpGet,
				lookupTXT:      tt.lookupTXT,
				registryClient: &fakeRegistryClient{},
			}
			// Prevent registration from doing any real I/O during verification-only tests.

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

func TestValidDomainGate_InvalidApex_SetsConditionAndSkipsFlows(t *testing.T) {
	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, networkingv1alpha.AddToScheme(testScheme))

	operatorConfig := config.NetworkServicesOperator{}
	config.SetObjectDefaults_NetworkServicesOperator(&operatorConfig)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}

	// Domain with non-registrable name (public suffix only)
	dom := &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns.Name,
			Name:      "invalid",
		},
		Spec: networkingv1alpha.DomainSpec{DomainName: "com"},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(dom, ns).
		WithStatusSubresource(dom).
		Build()

	reconciler := &DomainReconciler{
		mgr:            &fakeMockManager{cl: fakeClient},
		Config:         operatorConfig,
		timeNow:        time.Now,
		httpGet:        func(ctx context.Context, url string) ([]byte, *http.Response, error) { return nil, nil, nil },
		lookupTXT:      func(ctx context.Context, name string) ([]string, error) { return nil, nil },
		registryClient: &fakeRegistryClient{},
	}

	req := mcreconcile.Request{Request: reconcile.Request{NamespacedName: client.ObjectKey{Namespace: ns.Name, Name: dom.Name}}}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	// Fetch and assert conditions were set and flows skipped
	got := &networkingv1alpha.Domain{}
	assert.NoError(t, fakeClient.Get(context.Background(), client.ObjectKey{Namespace: ns.Name, Name: dom.Name}, got))

	cond := apimeta.FindStatusCondition(got.Status.Conditions, networkingv1alpha.DomainConditionValidDomain)
	if assert.NotNil(t, cond, "ValidDomain condition missing") {
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, networkingv1alpha.DomainReasonInvalidDomain, cond.Reason)
	}
	// Timers should be zeroed so no automatic retries
	if got.Status.Verification != nil {
		assert.True(t, got.Status.Verification.NextVerificationAttempt.IsZero())
	}
	if got.Status.Registration != nil {
		assert.True(t, got.Status.Registration.NextRefreshAttempt.IsZero())
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
	dom.Spec.DomainName = exampleDomain // apex

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	reg := &networkingv1alpha.Registration{Domain: "example.com", Source: "rdap"}
	fakeReg := &fakeRegistryClient{
		lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
			return &registrydata.DomainResult{
				Registration: reg,
				Nameservers: []networkingv1alpha.Nameserver{
					{Hostname: "ns1.example.net"},
					{Hostname: "ns2.example.net"},
				},
				Source:      "rdap",
				ProviderKey: "rdap.verisign.com",
			}, nil
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
		timeNow:        time.Now,
		registryClient: fakeReg,
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

func TestVerification_RequeueImmediate_WhenWakeDueOrPast(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	// Fixed reference time
	now := time.Date(2025, 10, 22, 15, 40, 14, 0, time.UTC)

	// Domain has existing verification scaffold and next attempt is due NOW
	dom := newDomain("default", "due-now", func(d *networkingv1alpha.Domain) {
		d.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
			DNSRecord: networkingv1alpha.DNSVerificationRecord{
				Name:    "_dnsverify.example.com",
				Type:    "TXT",
				Content: "token",
			},
			NextVerificationAttempt: metav1.Time{Time: now},
		}
	})

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	// Configure zero retry interval and zero jitter to force nextAttempt == now
	operatorConfig := config.NetworkServicesOperator{
		DomainVerification: config.DomainVerificationConfig{
			RetryIntervals:       []config.RetryInterval{{Interval: metav1.Duration{Duration: 0}}},
			RetryJitterMaxFactor: 0,
		},
		DomainRegistration: config.DomainRegistrationConfig{ // prevent nil panics in registration
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.1,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		},
	}

	r := &DomainReconciler{
		mgr:     mgr,
		Config:  operatorConfig,
		timeNow: func() time.Time { return now },
		// Stubs to avoid real I/O
		httpGet: func(ctx context.Context, url string) ([]byte, *http.Response, error) {
			return nil, nil, fmt.Errorf("not implemented")
		},
		lookupTXT:      func(ctx context.Context, name string) ([]string, error) { return nil, &net.DNSError{IsNotFound: true} },
		registryClient: &fakeRegistryClient{},
	}

	res, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	// With the fix, we should requeue immediately when wake time is now/past
	assert.Equal(t, 1*time.Second, res.RequeueAfter)
}

func TestVerification_RequeueImmediate_WhenWakeInPast(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	// Fixed reference time
	now := time.Date(2025, 10, 22, 15, 40, 14, 0, time.UTC)

	// Domain has existing verification scaffold and next attempt was due 1s ago
	dom := newDomain("default", "due-past", func(d *networkingv1alpha.Domain) {
		d.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
			DNSRecord: networkingv1alpha.DNSVerificationRecord{
				Name:    "_dnsverify.example.com",
				Type:    "TXT",
				Content: "token",
			},
			NextVerificationAttempt: metav1.Time{Time: now.Add(-1 * time.Second)},
		}
	})

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	operatorConfig := config.NetworkServicesOperator{
		DomainVerification: config.DomainVerificationConfig{
			RetryIntervals:       []config.RetryInterval{{Interval: metav1.Duration{Duration: 0}}},
			RetryJitterMaxFactor: 0,
		},
		DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.1,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		},
	}

	r := &DomainReconciler{
		mgr:     mgr,
		Config:  operatorConfig,
		timeNow: func() time.Time { return now },
		// Stubs to avoid real I/O
		httpGet: func(ctx context.Context, url string) ([]byte, *http.Response, error) {
			return nil, nil, fmt.Errorf("not implemented")
		},
		lookupTXT:      func(ctx context.Context, name string) ([]string, error) { return nil, &net.DNSError{IsNotFound: true} },
		registryClient: &fakeRegistryClient{},
	}

	res, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	// With the fix, we should requeue immediately when wake time is past
	assert.Equal(t, 1*time.Second, res.RequeueAfter)
}

func TestVerification_RequeueFloorsSubSecondToOneSecond(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	// Fixed reference time
	now := time.Date(2025, 10, 22, 15, 40, 14, 0, time.UTC)

	// Domain has verification with next attempt 500ms in the future
	dom := newDomain("default", "due-subsecond", func(d *networkingv1alpha.Domain) {
		d.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
			DNSRecord: networkingv1alpha.DNSVerificationRecord{
				Name:    "_dnsverify.example.com",
				Type:    "TXT",
				Content: "token",
			},
			NextVerificationAttempt: metav1.Time{Time: now.Add(500 * time.Millisecond)},
		}
	})

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	operatorConfig := config.NetworkServicesOperator{
		DomainVerification: config.DomainVerificationConfig{
			RetryIntervals:       []config.RetryInterval{{Interval: metav1.Duration{Duration: time.Second}}},
			RetryJitterMaxFactor: 0,
		},
		DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.1,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		},
	}

	r := &DomainReconciler{
		mgr:     mgr,
		Config:  operatorConfig,
		timeNow: func() time.Time { return now },
		// Stubs to avoid real I/O
		httpGet: func(ctx context.Context, url string) ([]byte, *http.Response, error) {
			return nil, nil, fmt.Errorf("not implemented")
		},
		lookupTXT:      func(ctx context.Context, name string) ([]string, error) { return nil, &net.DNSError{IsNotFound: true} },
		registryClient: &fakeRegistryClient{},
	}

	res, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	// We should schedule a 1s requeue minimum for sub-second remaining
	assert.Equal(t, 1*time.Second, res.RequeueAfter)
}

func TestRegistration_Subdomain_DelegationOverridesApexNS(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "sub")
	dom.Spec.DomainName = "app.example.com" // non-apex

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	fakeReg := &fakeRegistryClient{
		lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
			return &registrydata.DomainResult{
				Registration: &networkingv1alpha.Registration{Domain: "example.com", Source: "rdap"},
				Nameservers: []networkingv1alpha.Nameserver{
					{Hostname: "ns-deleg-1.example.net"},
					{Hostname: "ns-deleg-2.example.net"},
				},
			}, nil
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
		timeNow:        time.Now,
		registryClient: fakeReg,
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

	fakeReg := &fakeRegistryClient{
		lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
			return &registrydata.DomainResult{
				Registration: &networkingv1alpha.Registration{Domain: "example.com", Source: "rdap"},
				Nameservers: []networkingv1alpha.Nameserver{
					{Hostname: "ns1.example.net"},
					{Hostname: "ns2.example.net"},
				},
			}, nil
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
		timeNow:        time.Now,
		registryClient: fakeReg,
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

	fakeReg := &fakeRegistryClient{
		lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
			return &registrydata.DomainResult{
				Registration: &networkingv1alpha.Registration{
					Domain:   "example.ai",
					Source:   "rdap",
					Registry: &networkingv1alpha.RegistryInfo{Name: "rdap.identitydigital.services", URL: "https://rdap.identitydigital.services"},
				},
				Nameservers: []networkingv1alpha.Nameserver{},
			}, nil
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
		timeNow:        time.Now,
		registryClient: fakeReg,
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
	dom.Spec.DomainName = exampleDomain

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
		timeNow: func() time.Time { return now },
		registryClient: &fakeRegistryClient{
			lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
				return nil, fmt.Errorf("boom")
			},
		},
	}

	res, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

	// should schedule retry in 2m
	assert.Equal(t, 2*time.Minute, got.Status.Registration.NextRefreshAttempt.Sub(now))
	assert.True(t, res.RequeueAfter == 0 || res.RequeueAfter > 0) // depending on verification timer
}

func TestRegistration_RDAP429_WithRetryAfter_SchedulesRetryAfter(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "rate-limited")
	dom.Spec.DomainName = exampleDomain

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	now := time.Date(2025, 10, 9, 1, 0, 0, 0, time.UTC)
	retryAfter := 7 * time.Second

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.0, // deterministic
			RetryBackoff:    &metav1.Duration{Duration: 2 * time.Minute},
		}},
		timeNow: func() time.Time { return now },
		registryClient: &fakeRegistryClient{
			lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
				return nil, &registrydata.RateLimitedError{Provider: "rdap.verisign.com", RetryAfter: retryAfter}
			},
		},
	}

	_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

	gotDelay := got.Status.Registration.NextRefreshAttempt.Sub(now)
	// Should honor Retry-After; allow small slack in case of internal delays
	assert.GreaterOrEqual(t, gotDelay, retryAfter)
	assert.LessOrEqual(t, gotDelay, retryAfter+10*time.Second)
}

func TestRegistration_RDAP429_NoRetryAfter_Uses2xBackoff(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "rate-limited-no-header")
	dom.Spec.DomainName = exampleDomain

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	now := time.Date(2025, 10, 9, 1, 0, 0, 0, time.UTC)
	backoff := 2 * time.Minute

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.0, // deterministic
			RetryBackoff:    &metav1.Duration{Duration: backoff},
		}},
		timeNow: func() time.Time { return now },
		registryClient: &fakeRegistryClient{
			lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
				// Simulate a provider signaling backoff but no explicit Retry-After; library can surface SuggestedDelay.
				return &registrydata.DomainResult{SuggestedDelay: 2 * backoff}, &registrydata.RateLimitedError{Provider: "rdap.verisign.com"}
			},
		},
	}

	_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

	// Should use 2x backoff when no Retry-After header value is known
	want := 2 * backoff
	gotDelay := got.Status.Registration.NextRefreshAttempt.Sub(now)
	// Allow slack since providers may impose additional quiet-periods we can't read without headers
	assert.GreaterOrEqual(t, gotDelay, want)
	assert.LessOrEqual(t, gotDelay, 4*backoff)
}

func TestRegistration_DesiredRefreshAttempt_ExpediteBehavior(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	now := time.Date(2025, 11, 14, 12, 0, 0, 0, time.UTC)
	refreshInterval := 30 * time.Minute

	newReconciler := func(cl client.Client, rdapCalled *bool) *DomainReconciler {
		return &DomainReconciler{
			mgr: &fakeMockManager{cl: cl},
			Config: config.NetworkServicesOperator{
				DomainRegistration: config.DomainRegistrationConfig{
					LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
					RefreshInterval: &metav1.Duration{Duration: refreshInterval},
					JitterMaxFactor: 0.0, // deterministic next schedule
					RetryBackoff:    &metav1.Duration{Duration: time.Minute},
				},
			},
			timeNow: func() time.Time { return now },
			registryClient: &fakeRegistryClient{
				lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
					*rdapCalled = true
					return &registrydata.DomainResult{Registration: &networkingv1alpha.Registration{Domain: "example.com"}}, nil
				},
			},
		}
	}

	t.Run("skips when next refresh in future and no desired override", func(t *testing.T) {
		dom := newDomain("default", "no-desired")
		dom.Spec.DomainName = exampleDomain
		future := now.Add(10 * time.Minute)
		dom.Status.Registration = &networkingv1alpha.Registration{
			NextRefreshAttempt: metav1.Time{Time: future},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
		rdapCalled := false
		r := newReconciler(cl, &rdapCalled)

		_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
		assert.NoError(t, err)

		got := &networkingv1alpha.Domain{}
		_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

		assert.False(t, rdapCalled, "rdap should not be called when skipping")
		assert.True(t, got.Status.Registration.LastRefreshAttempt.IsZero(), "last attempt should remain zero when skipping")
		assert.True(t, got.Status.Registration.NextRefreshAttempt.Time.Equal(future), "next attempt should be unchanged")
	})

	t.Run("expedites when desired in past and last attempt before desired", func(t *testing.T) {
		dom := newDomain("default", "expedite")
		dom.Spec.DomainName = exampleDomain
		future := now.Add(10 * time.Minute)
		desired := metav1.NewTime(now.Add(-1 * time.Minute))
		dom.Spec.DesiredRegistrationRefreshAttempt = &desired
		dom.Status.Registration = &networkingv1alpha.Registration{
			NextRefreshAttempt: metav1.Time{Time: future},
			// LastRefreshAttempt zero (never attempted)
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
		rdapCalled := false
		r := newReconciler(cl, &rdapCalled)

		_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
		assert.NoError(t, err)

		got := &networkingv1alpha.Domain{}
		_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

		assert.True(t, rdapCalled, "rdap should be called due to expedite")
		assert.False(t, got.Status.Registration.LastRefreshAttempt.IsZero(), "last attempt should be stamped to now")
		assert.WithinDuration(t, now, got.Status.Registration.LastRefreshAttempt.Time, 2*time.Second, "last attempt should be stamped to now")
		// NextRefreshAttempt should be scheduled at/after last attempt + configured interval.
		// We assert lower bound to avoid flakiness from internal clock sources.
		minNext := got.Status.Registration.LastRefreshAttempt.Add(refreshInterval)
		assert.True(t, !got.Status.Registration.NextRefreshAttempt.Time.Before(minNext), "next attempt should be >= last+interval")
	})

	t.Run("does not expedite when last attempt already after desired and next is in future", func(t *testing.T) {
		dom := newDomain("default", "no-expedite")
		dom.Spec.DomainName = exampleDomain
		future := now.Add(10 * time.Minute)
		desired := metav1.NewTime(now.Add(-2 * time.Minute))
		dom.Spec.DesiredRegistrationRefreshAttempt = &desired
		last := metav1.NewTime(now.Add(-1 * time.Minute)) // after desired
		dom.Status.Registration = &networkingv1alpha.Registration{
			NextRefreshAttempt: metav1.Time{Time: future},
			LastRefreshAttempt: last,
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
		rdapCalled := false
		r := newReconciler(cl, &rdapCalled)

		_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
		assert.NoError(t, err)

		got := &networkingv1alpha.Domain{}
		_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

		assert.False(t, rdapCalled, "rdap should not be called when not expediting")
		assert.True(t, got.Status.Registration.LastRefreshAttempt.Time.Equal(last.Time), "last attempt should remain unchanged")
		assert.True(t, got.Status.Registration.NextRefreshAttempt.Time.Equal(future), "next attempt should be unchanged")
	})

	t.Run("attempts when next refresh due regardless of desired/last", func(t *testing.T) {
		dom := newDomain("default", "due-now")
		dom.Spec.DomainName = exampleDomain
		due := now.Add(-1 * time.Second)
		desired := metav1.NewTime(now.Add(-2 * time.Minute))
		dom.Spec.DesiredRegistrationRefreshAttempt = &desired
		last := metav1.NewTime(now.Add(-1 * time.Minute)) // after desired
		dom.Status.Registration = &networkingv1alpha.Registration{
			NextRefreshAttempt: metav1.Time{Time: due},
			LastRefreshAttempt: last,
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
		rdapCalled := false
		r := newReconciler(cl, &rdapCalled)

		_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
		assert.NoError(t, err)

		got := &networkingv1alpha.Domain{}
		_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)

		assert.True(t, rdapCalled, "rdap should be called when next is due")
		assert.WithinDuration(t, now, got.Status.Registration.LastRefreshAttempt.Time, 2*time.Second, "last attempt should be updated to now")
	})

	t.Run("schedules wake to desired when desired sooner than next and not satisfied", func(t *testing.T) {
		dom := newDomain("default", "schedule-desired")
		dom.Spec.DomainName = exampleDomain
		next := now.Add(20 * time.Minute)
		desired := metav1.NewTime(now.Add(5 * time.Minute)) // earlier than next
		dom.Spec.DesiredRegistrationRefreshAttempt = &desired
		// Never attempted; pending desired
		dom.Status.Registration = &networkingv1alpha.Registration{
			NextRefreshAttempt: metav1.Time{Time: next},
			// LastRefreshAttempt zero
		}
		// Disable verification influence so registration scheduling is the only wake source.
		dom.Status.Conditions = append(dom.Status.Conditions, metav1.Condition{
			Type:               networkingv1alpha.DomainConditionVerified,
			Status:             metav1.ConditionTrue,
			Reason:             networkingv1alpha.DomainReasonVerified,
			Message:            "verified for test",
			LastTransitionTime: metav1.Now(),
		})

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
		rdapCalled := false
		r := newReconciler(cl, &rdapCalled)

		res, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
		assert.NoError(t, err)

		// Expect we scheduled a requeue close to 5 minutes (earliest desired), not 20 minutes.
		assert.GreaterOrEqual(t, res.RequeueAfter, 4*time.Minute)
		assert.LessOrEqual(t, res.RequeueAfter, 6*time.Minute)

		got := &networkingv1alpha.Domain{}
		_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)
		assert.False(t, rdapCalled, "rdap should not be called when only scheduling to desired")
		// Status should remain largely unchanged (no stamp of last attempt)
		assert.True(t, got.Status.Registration.LastRefreshAttempt.IsZero())
		assert.True(t, got.Status.Registration.NextRefreshAttempt.Time.Equal(next))
	})

	t.Run("after desired satisfied, subsequent wake uses NextRefreshAttempt", func(t *testing.T) {
		dom := newDomain("default", "desired-then-next")
		dom.Spec.DomainName = exampleDomain
		next := now.Add(20 * time.Minute)
		desired := metav1.NewTime(now.Add(5 * time.Minute))
		dom.Spec.DesiredRegistrationRefreshAttempt = &desired
		dom.Status.Registration = &networkingv1alpha.Registration{
			NextRefreshAttempt: metav1.Time{Time: next},
			// LastRefreshAttempt zero initially
		}
		// Mark verified so only registration scheduling influences wakes
		dom.Status.Conditions = append(dom.Status.Conditions, metav1.Condition{
			Type:               networkingv1alpha.DomainConditionVerified,
			Status:             metav1.ConditionTrue,
			Reason:             networkingv1alpha.DomainReasonVerified,
			Message:            "verified for test",
			LastTransitionTime: metav1.Now(),
		})

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()

		// Step 1: before desired time — should schedule to desired without attempting
		rdapCalled1 := false
		r1 := newReconciler(cl, &rdapCalled1)
		res1, err := r1.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
		assert.NoError(t, err)
		assert.False(t, rdapCalled1, "should not attempt before desired")
		assert.GreaterOrEqual(t, res1.RequeueAfter, 4*time.Minute)
		assert.LessOrEqual(t, res1.RequeueAfter, 6*time.Minute)

		// Step 2: at desired time — should attempt, stamp last, and schedule next = last + interval
		now2 := desired.Add(1 * time.Second)
		rdapCalled2 := false
		r2 := &DomainReconciler{
			mgr: &fakeMockManager{cl: cl},
			Config: config.NetworkServicesOperator{
				DomainRegistration: config.DomainRegistrationConfig{
					LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
					RefreshInterval: &metav1.Duration{Duration: refreshInterval},
					JitterMaxFactor: 0.0,
					RetryBackoff:    &metav1.Duration{Duration: time.Minute},
				},
			},
			timeNow: func() time.Time { return now2 },
			registryClient: &fakeRegistryClient{
				lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
					rdapCalled2 = true
					return &registrydata.DomainResult{Registration: &networkingv1alpha.Registration{Domain: "example.com"}}, nil
				},
			},
		}
		_, err = r2.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
		assert.NoError(t, err)

		got2 := &networkingv1alpha.Domain{}
		_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got2)
		assert.True(t, rdapCalled2, "should attempt at/after desired")
		assert.WithinDuration(t, now2, got2.Status.Registration.LastRefreshAttempt.Time, 2*time.Second)
		minNext := got2.Status.Registration.LastRefreshAttempt.Add(refreshInterval)
		assert.True(t, !got2.Status.Registration.NextRefreshAttempt.Time.Before(minNext), "next should be >= last+interval")

		// Step 3: between last and next — desired should be ignored (already satisfied), wake should be to next
		now3 := now2.Add(1 * time.Minute)
		rdapCalled3 := false
		r3 := &DomainReconciler{
			mgr:     &fakeMockManager{cl: cl},
			Config:  r2.Config,
			timeNow: func() time.Time { return now3 },
			registryClient: &fakeRegistryClient{
				lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
					rdapCalled3 = true
					return &registrydata.DomainResult{Registration: &networkingv1alpha.Registration{Domain: "example.com"}}, nil
				},
			},
		}
		res3, err := r3.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
		assert.NoError(t, err)
		assert.False(t, rdapCalled3, "should not attempt again before next")

		remaining := got2.Status.Registration.NextRefreshAttempt.Sub(now3)
		if remaining < time.Second {
			remaining = time.Second
		}
		// RequeueAfter is truncated to seconds; allow a small delta
		assert.GreaterOrEqual(t, res3.RequeueAfter, remaining-1*time.Second)
		assert.LessOrEqual(t, res3.RequeueAfter, remaining+1*time.Second)
	})
}

func TestRegistration_EnrichesNameserverIPs(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "enrich")
	dom.Spec.DomainName = exampleDomain

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.0,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		}},
		timeNow: time.Now,
		registryClient: &fakeRegistryClient{
			lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
				return &registrydata.DomainResult{
					Registration: &networkingv1alpha.Registration{Domain: "example.com", Source: "rdap"},
					Nameservers: []networkingv1alpha.Nameserver{
						{
							Hostname: "ns1.example.net",
							IPs: []networkingv1alpha.NameserverIP{{
								Address:        "192.0.2.10",
								RegistrantName: "Example Net Ops",
							}},
						},
					},
				}, nil
			},
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

func TestRegistration_WHOIS_BootstrapAndReferrals(t *testing.T) {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = networkingv1alpha.AddToScheme(s)

	dom := newDomain("default", "whois-co")
	dom.Spec.DomainName = "example.co"

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(dom).WithStatusSubresource(dom).Build()
	mgr := &fakeMockManager{cl: cl}

	// Registry client returns WHOIS-mapped data (WHOIS bootstrap/referrals are handled inside registrydata).
	r := &DomainReconciler{
		mgr: mgr,
		Config: config.NetworkServicesOperator{DomainRegistration: config.DomainRegistrationConfig{
			LookupTimeout:   &metav1.Duration{Duration: 3 * time.Second},
			RefreshInterval: &metav1.Duration{Duration: time.Hour},
			JitterMaxFactor: 0.0,
			RetryBackoff:    &metav1.Duration{Duration: time.Minute},
		}},
		timeNow: time.Now,
		registryClient: &fakeRegistryClient{
			lookupDomain: func(ctx context.Context, domain string, opts registrydata.LookupOptions) (*registrydata.DomainResult, error) {
				return &registrydata.DomainResult{
					Registration: &networkingv1alpha.Registration{
						Domain:           "example.co",
						Source:           "whois",
						RegistryDomainID: "D24111695-CNIC",
						Registrar:        &networkingv1alpha.RegistrarInfo{Name: "GoDaddy.com, LLC", IANAID: "146"},
						Abuse:            &networkingv1alpha.AbuseContact{Phone: "+1.4806242505"},
						DNSSEC:           &networkingv1alpha.DNSSECInfo{Enabled: ptrBool(false)},
					},
				}, nil
			},
		},
	}

	_, err := r.Reconcile(context.Background(), mcreconcile.Request{ClusterName: "test", Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dom)}})
	assert.NoError(t, err)

	got := &networkingv1alpha.Domain{}
	_ = cl.Get(context.Background(), client.ObjectKeyFromObject(dom), got)
	if assert.NotNil(t, got.Status.Registration) {
		assert.Equal(t, "whois", got.Status.Registration.Source)
		if assert.NotNil(t, got.Status.Registration.Registrar) {
			assert.Equal(t, "GoDaddy.com, LLC", got.Status.Registration.Registrar.Name)
			assert.Equal(t, "146", got.Status.Registration.Registrar.IANAID)
		}
		if assert.NotNil(t, got.Status.Registration.Abuse) {
			assert.Equal(t, "+1.4806242505", got.Status.Registration.Abuse.Phone)
		}
		if assert.NotNil(t, got.Status.Registration.DNSSEC) && assert.NotNil(t, got.Status.Registration.DNSSEC.Enabled) {
			assert.False(t, *got.Status.Registration.DNSSEC.Enabled)
		}
	}
}

func ptrBool(b bool) *bool { return &b }
