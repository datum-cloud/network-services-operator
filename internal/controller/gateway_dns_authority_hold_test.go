// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// A Domain's nameservers are refreshed once a day, so one wrong answer from the
// registry takes authority away until the next refresh. These tests pin how
// Gateway DNS treats the record set of a hostname it still claims during that
// time: kept unchanged for the configured period, then deleted.

const (
	holdNamespace = "test-ns"
	holdHostname  = "api.example.com"
	holdPeriod    = 48 * time.Hour
)

// authorityHoldFixture is one gateway, one verified Domain with a ready DNSZone,
// and optionally the gateway's existing record set for holdHostname.
type authorityHoldFixture struct {
	ctx        context.Context
	client     client.Client
	reconciler *GatewayReconciler
	gateway    *gatewayv1.Gateway
	rsKey      client.ObjectKey
}

type holdSetup struct {
	authority bool          // the Domain's nameservers overlap the DNSZone's
	recordSet bool          // the gateway already has a record set for the hostname
	stamp     string        // the record set's authority-missing-since annotation
	created   time.Duration // how long ago the record set was created
	period    *time.Duration
	noDomain  bool   // the Domain was deleted
	noZone    bool   // the DNSZone was deleted
	managedBy string // the record set's managed-by label when another manager owns it
}

func newAuthorityHoldFixture(t *testing.T, setup holdSetup) authorityHoldFixture {
	t.Helper()
	period := holdPeriod
	if setup.period != nil {
		period = *setup.period
	}
	cfg := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:                "gateways.test.local",
			EnableDNSIntegration:        true,
			DNSAuthorityLossGracePeriod: &metav1.Duration{Duration: period},
		},
	}

	gw := newTestGatewayForDNS(holdNamespace, "my-gw")
	domain := newVerifiedDNSZoneDomain(holdNamespace, "example.com", false)
	if !setup.authority {
		// The answer of 2026-07-10: a successful lookup that names other nameservers.
		domain.Status.Nameservers = []networkingv1alpha.Nameserver{{Hostname: "ns1.registry-parent.example"}}
	}
	zone := newDNSZone(holdNamespace, "example-com", "example.com")
	objects := []client.Object{gw}
	if !setup.noDomain {
		objects = append(objects, domain)
	}
	if !setup.noZone {
		objects = append(objects, zone)
	}

	rsKey := client.ObjectKey{Namespace: holdNamespace, Name: dnsRecordSetName(gw.Name, holdHostname)}
	if setup.recordSet {
		objects = append(objects, newHeldRecordSet(gw, rsKey.Name, zone.Name, setup))
	}
	for _, obj := range objects {
		if ts := obj.GetCreationTimestamp(); ts.Time.IsZero() {
			obj.SetCreationTimestamp(metav1.Now())
		}
	}

	return authorityHoldFixture{
		ctx:        log.IntoContext(context.Background(), zap.New()),
		client:     buildFakeUpstreamClientForDNS(newDNSTestScheme(t), objects...),
		reconciler: newDNSReconciler(cfg),
		gateway:    gw,
		rsKey:      rsKey,
	}
}

func newHeldRecordSet(gw *gatewayv1.Gateway, name, zoneName string, setup holdSetup) *dnsv1alpha1.DNSRecordSet {
	rs := &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         gw.Namespace,
			Name:              name,
			UID:               uuid.NewUUID(),
			CreationTimestamp: metav1.NewTime(time.Now().Add(-setup.created)),
			Labels: map[string]string{
				labelManagedBy:     labelManagedByValue,
				labelDNSManaged:    labelValueTrue,
				labelDNSSourceKind: KindGateway,
				labelDNSSourceName: gw.Name,
				labelDNSSourceNS:   gw.Namespace,
			},
			Annotations: map[string]string{annotationDNSHostname: holdHostname},
			// As Gateway DNS writes it, so the garbage collector deletes it with the gateway.
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gatewayv1.GroupVersion.String(), Kind: KindGateway, Name: gw.Name, UID: gw.UID,
			}},
		},
		Spec: dnsv1alpha1.DNSRecordSetSpec{
			DNSZoneRef: corev1.LocalObjectReference{Name: zoneName},
			RecordType: dnsv1alpha1.RRTypeCNAME,
			Records: []dnsv1alpha1.RecordEntry{{
				Name:  "api",
				CNAME: &dnsv1alpha1.CNAMERecordSpec{Content: "old-canonical.gateways.test.local."},
			}},
		},
	}
	if setup.stamp != "" {
		rs.Annotations[annotationDNSAuthorityMissingSince] = setup.stamp
	}
	if setup.managedBy != "" {
		rs.Labels[labelManagedBy] = setup.managedBy
		rs.OwnerReferences = nil
	}
	return rs
}

// reconcile runs one pass for the given claimed hostnames and returns the
// hostname's status condition and the recheck the pass asked for.
func (f authorityHoldFixture) reconcile(t *testing.T, claimed ...string) (*metav1.Condition, time.Duration) {
	t.Helper()
	statuses, result, recheck := f.reconciler.ensureDNSRecordSets(f.ctx, f.client, f.gateway, claimed)
	require.NoError(t, result.Err)
	for _, s := range statuses {
		if s.Hostname == holdHostname {
			return apimeta.FindStatusCondition(s.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed), recheck
		}
	}
	return nil, recheck
}

// recordSet returns the hostname's record set, or nil when it is gone.
func (f authorityHoldFixture) recordSet(t *testing.T) *dnsv1alpha1.DNSRecordSet {
	t.Helper()
	var rs dnsv1alpha1.DNSRecordSet
	err := f.client.Get(f.ctx, f.rsKey, &rs)
	if apierrors.IsNotFound(err) {
		return nil
	}
	require.NoError(t, err)
	return &rs
}

func stampAgo(d time.Duration) string {
	return time.Now().Add(-d).UTC().Format(time.RFC3339)
}

// (a) A blip keeps the record set, stamps it once, and does not rewrite it.
func TestDNSAuthorityHold_BlipKeepsAndStampsOnce(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true})

	before := time.Now().Add(-time.Second)
	cond, recheck := f.reconcile(t, holdHostname)

	rs := f.recordSet(t)
	require.NotNil(t, rs, "a claimed hostname without authority must keep its record set")
	stamp := rs.Annotations[annotationDNSAuthorityMissingSince]
	since, err := time.Parse(time.RFC3339, stamp)
	require.NoError(t, err, "the first pass without authority stamps an RFC 3339 time")
	assert.False(t, since.Before(before.Truncate(time.Second)), "the stamp is the time of this pass")
	assert.Equal(t, "old-canonical.gateways.test.local.", rs.Spec.Records[0].CNAME.Content,
		"programming pauses: the kept record set is not rewritten")
	assert.InDelta(t, holdPeriod.Seconds(), recheck.Seconds(), 5, "the pass rechecks when the period ends")
	require.NotNil(t, cond)
	assert.Equal(t, networkingv1alpha.DNSRecordReasonDNSAuthorityMissing, cond.Reason)
	assert.Contains(t, cond.Message, "kept until")

	// Move the stamp back an hour, as if that pass had run an hour ago; the next
	// pass must leave it as it is.
	earlier := stampAgo(time.Hour)
	patch := client.MergeFrom(rs.DeepCopy())
	rs.Annotations[annotationDNSAuthorityMissingSince] = earlier
	require.NoError(t, f.client.Patch(f.ctx, rs, patch))

	_, recheck = f.reconcile(t, holdHostname)
	rs = f.recordSet(t)
	require.NotNil(t, rs)
	assert.Equal(t, earlier, rs.Annotations[annotationDNSAuthorityMissingSince], "a later pass does not stamp again")
	assert.InDelta(t, (holdPeriod - time.Hour).Seconds(), recheck.Seconds(), 5)
}

// (b) Authority coming back removes the stamp and programs the record again.
func TestDNSAuthorityHold_AuthorityReturnRemovesStamp(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{authority: true, recordSet: true, stamp: stampAgo(time.Hour)})

	cond, recheck := f.reconcile(t, holdHostname)

	rs := f.recordSet(t)
	require.NotNil(t, rs)
	assert.NotContains(t, rs.Annotations, annotationDNSAuthorityMissingSince, "authority back ends the wait")
	assert.Equal(t, "api", rs.Spec.Records[0].Name, "programming resumes")
	assert.Zero(t, recheck, "nothing is held")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}

// Authority back while another manager's record holds the hostname: the
// conflict leaves the gateway's record set as it is, but the wait is over, so
// its stamp goes. A stamp left there would count from this loss at the next.
func TestDNSAuthorityHold_AuthorityReturnUnderConflictRemovesStamp(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{authority: true, recordSet: true, stamp: stampAgo(time.Hour)})
	users := newHeldRecordSet(f.gateway, "users-api", "example-com", holdSetup{managedBy: "user"})
	users.SetCreationTimestamp(metav1.Now())
	require.NoError(t, f.client.Create(f.ctx, users))

	cond, recheck := f.reconcile(t, holdHostname)

	require.NotNil(t, cond)
	assert.Equal(t, networkingv1alpha.DNSRecordReasonConflict, cond.Reason)
	rs := f.recordSet(t)
	require.NotNil(t, rs)
	assert.NotContains(t, rs.Annotations, annotationDNSAuthorityMissingSince, "authority back ends the wait")
	assert.Zero(t, recheck)
}

// (c) A stamp older than the period lets garbage collection delete the record set.
func TestDNSAuthorityHold_StampPastPeriodDeletes(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, stamp: stampAgo(holdPeriod + time.Hour)})

	cond, recheck := f.reconcile(t, holdHostname)

	assert.Nil(t, f.recordSet(t), "a lasting loss still deletes")
	assert.Zero(t, recheck)
	require.NotNil(t, cond)
	assert.Equal(t, networkingv1alpha.DNSRecordReasonDNSAuthorityMissing, cond.Reason)
	assert.NotContains(t, cond.Message, "kept until")
}

// (d) The #283 guard: a hostname the gateway no longer claims is deleted at
// once, even while its record set carries a stamp inside the period.
func TestDNSAuthorityHold_UnclaimedHostnameDeletesAtOnce(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, stamp: stampAgo(time.Hour)})

	_, recheck := f.reconcile(t) // the hostname was removed from the gateway

	assert.Nil(t, f.recordSet(t), "the pause applies only while the hostname stays claimed")
	assert.Zero(t, recheck)
}

// (e) A stamp written before a restart is counted from, not rewritten and not
// ignored: the wait neither restarts nor ends early.
func TestDNSAuthorityHold_RestartNeitherRestartsNorSkipsTheWait(t *testing.T) {
	stamp := stampAgo(10 * time.Hour)
	// The record set is older than the period, so counting from its creation
	// instead of from the stamp would delete it.
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, stamp: stamp, created: 72 * time.Hour})

	_, recheck := f.reconcile(t, holdHostname) // a new reconciler, nothing in memory

	rs := f.recordSet(t)
	require.NotNil(t, rs, "the wait is not skipped")
	assert.Equal(t, stamp, rs.Annotations[annotationDNSAuthorityMissingSince], "the wait is not restarted")
	assert.InDelta(t, (holdPeriod - 10*time.Hour).Seconds(), recheck.Seconds(), 5)
}

// An unreadable stamp restarts the wait rather than deleting on a value
// nothing can read, as NetworkPresence does.
func TestDNSAuthorityHold_UnreadableStampRestartsTheWait(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, stamp: "yesterday"})

	_, recheck := f.reconcile(t, holdHostname)

	rs := f.recordSet(t)
	require.NotNil(t, rs)
	_, err := time.Parse(time.RFC3339, rs.Annotations[annotationDNSAuthorityMissingSince])
	assert.NoError(t, err, "the stamp is rewritten as a readable time")
	assert.InDelta(t, holdPeriod.Seconds(), recheck.Seconds(), 5)
}

// A record set under the gateway's name that another manager owns is not the
// gateway's to keep: the hold neither stamps it nor waits on it.
func TestDNSAuthorityHold_ForeignRecordSetIsNotHeld(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, managedBy: "user"})

	cond, recheck := f.reconcile(t, holdHostname)

	rs := f.recordSet(t)
	require.NotNil(t, rs, "garbage collection lists only the gateway's own record sets")
	assert.NotContains(t, rs.Annotations, annotationDNSAuthorityMissingSince, "nothing is written to it")
	assert.Zero(t, recheck)
	require.NotNil(t, cond)
	assert.NotContains(t, cond.Message, "kept until")
}

// The hold keeps what exists; it never creates a record set without authority.
func TestDNSAuthorityHold_NoRecordSetCreatesNothing(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{})

	cond, recheck := f.reconcile(t, holdHostname)

	assert.Nil(t, f.recordSet(t))
	assert.Zero(t, recheck)
	require.NotNil(t, cond)
	assert.Equal(t, networkingv1alpha.DNSRecordReasonDNSAuthorityMissing, cond.Reason)
}

// A zero period keeps the earlier behaviour: the first check without
// authority deletes.
func TestDNSAuthorityHold_ZeroPeriodDeletesAtFirstCheck(t *testing.T) {
	zero := time.Duration(0)
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, period: &zero})

	_, recheck := f.reconcile(t, holdHostname)

	assert.Nil(t, f.recordSet(t))
	assert.Zero(t, recheck)
}

// Deleting the gateway, or the HTTPProxy that owns it, deletes its record sets
// at once, stamped or not: finalization lists them by label, and the stamp
// changes neither the labels nor the owner reference the garbage collector uses.
func TestDNSAuthorityHold_GatewayFinalizationDeletesStampedRecordSet(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true})
	_, _ = f.reconcile(t, holdHostname) // stamps it
	rs := f.recordSet(t)
	require.NotNil(t, rs)
	require.NotEmpty(t, rs.Annotations[annotationDNSAuthorityMissingSince])
	require.Len(t, rs.OwnerReferences, 1, "stamping keeps the gateway's owner reference")
	assert.Equal(t, f.gateway.UID, rs.OwnerReferences[0].UID)

	result := f.reconciler.cleanupDNSRecordSets(f.ctx, f.client, f.gateway)

	require.NoError(t, result.Err)
	assert.Nil(t, f.recordSet(t), "finalization does not wait for the period")
}

// A deleted Domain or DNSZone is not a loss of authority: nothing is held, and
// the record set goes at once, stamped or not.
func TestDNSAuthorityHold_DomainOrZoneDeletedDeletesAtOnce(t *testing.T) {
	for name, setup := range map[string]holdSetup{
		"domain deleted":  {recordSet: true, stamp: stampAgo(time.Hour), noDomain: true},
		"dnszone deleted": {recordSet: true, stamp: stampAgo(time.Hour), noZone: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityHoldFixture(t, setup)

			cond, recheck := f.reconcile(t, holdHostname)

			assert.Nil(t, f.recordSet(t))
			assert.Zero(t, recheck)
			require.NotNil(t, cond)
			assert.NotEqual(t, networkingv1alpha.DNSRecordReasonDNSAuthorityMissing, cond.Reason)
		})
	}
}

// The period is read at every pass and counted from the stamp, so a change in
// config moves the deadline of record sets already kept, down to zero.
func TestDNSAuthorityHold_PeriodChangeMovesTheDeadline(t *testing.T) {
	for name, tc := range map[string]struct {
		stampAge, period time.Duration
		kept             bool
	}{
		"lowered below the stamp's age deletes": {stampAge: 10 * time.Hour, period: 5 * time.Hour},
		"raised keeps it longer":                {stampAge: 50 * time.Hour, period: 72 * time.Hour, kept: true},
		"zero deletes a stamped record set":     {stampAge: time.Hour, period: 0},
	} {
		t.Run(name, func(t *testing.T) {
			period := tc.period
			f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, stamp: stampAgo(tc.stampAge), period: &period})

			_, recheck := f.reconcile(t, holdHostname)

			assert.Equal(t, tc.kept, f.recordSet(t) != nil)
			if tc.kept {
				assert.InDelta(t, (tc.period - tc.stampAge).Seconds(), recheck.Seconds(), 5)
			}
		})
	}
}

// Many passes over one kept record set, some at once, leave one record set
// and one stamp: the stamp it had, or one these passes wrote when it had none.
// None of them deletes it. The passes share one reconciler, as the workers of
// the controller do, so -race sees any state a pass writes on it.
func TestDNSAuthorityHold_ConcurrentPassesKeepTheStamp(t *testing.T) {
	for name, stamp := range map[string]string{"stamped": stampAgo(time.Hour), "unstamped": ""} {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, stamp: stamp})
			start := time.Now().Truncate(time.Second)

			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range 20 {
						_, result, _ := f.reconciler.ensureDNSRecordSets(f.ctx, f.client, f.gateway, []string{holdHostname})
						assert.NoError(t, result.Err)
					}
				}()
			}
			wg.Wait()

			rs := f.recordSet(t)
			require.NotNil(t, rs)
			got := rs.Annotations[annotationDNSAuthorityMissingSince]
			if stamp != "" {
				assert.Equal(t, stamp, got)
				return
			}
			since, err := time.Parse(time.RFC3339, got)
			require.NoError(t, err)
			assert.False(t, since.Before(start) || since.After(time.Now()), "a stamp these passes wrote")
		})
	}
}

// A failed stamp fails the pass before garbage collection, so an API error
// never turns a kept record set into a deleted one.
func TestDNSAuthorityHold_StampWriteErrorDeletesNothing(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true})
	failing := interceptor.NewClient(f.client.(client.WithWatch), interceptor.Funcs{
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return apierrors.NewServiceUnavailable("etcd is slow")
		},
	})

	_, result, _ := f.reconciler.ensureDNSRecordSets(f.ctx, failing, f.gateway, []string{holdHostname})

	require.Error(t, result.Err, "the pass reports the failed stamp")
	rs := f.recordSet(t)
	require.NotNil(t, rs, "nothing is deleted on a failed stamp")
	assert.NotContains(t, rs.Annotations, annotationDNSAuthorityMissingSince)
}

// Two kept record sets: the pass rechecks when the earlier wait ends, so the
// first expiry is not missed while the later one is still waiting.
func TestDNSAuthorityHold_TwoKeptRecordSetsRecheckAtTheEarlierEnd(t *testing.T) {
	f := newAuthorityHoldFixture(t, holdSetup{recordSet: true, stamp: stampAgo(time.Hour)})
	other := "www.example.com"
	second := newHeldRecordSet(f.gateway, dnsRecordSetName(f.gateway.Name, other), "example-com",
		holdSetup{stamp: stampAgo(10 * time.Hour)})
	second.Annotations[annotationDNSHostname] = other
	second.SetCreationTimestamp(metav1.Now())
	require.NoError(t, f.client.Create(f.ctx, second))

	_, result, recheck := f.reconciler.ensureDNSRecordSets(f.ctx, f.client, f.gateway, []string{holdHostname, other})

	require.NoError(t, result.Err)
	assert.InDelta(t, (holdPeriod - 10*time.Hour).Seconds(), recheck.Seconds(), 5, "the earlier end wins")
}

// The gateway's reconcile hands the controller the hold's recheck, so the wait's
// end needs no other event to delete the record set.
func TestDNSAuthorityHold_GatewayReconcileRequeuesAtTheEnd(t *testing.T) {
	rig := newClaimRig(t, func([]gatewayv1.Listener) []gatewayv1.Listener {
		return []gatewayv1.Listener{claimRigListener}
	})
	require.NoError(t, rig.downstream.Delete(rig.ctx, rig.heldClaim), "the hostname is free, so the gateway claims it")
	rig.reconciler.Config.Gateway.EnableDNSIntegration = true
	rig.reconciler.Config.Gateway.DNSAuthorityLossGracePeriod = &metav1.Duration{Duration: holdPeriod}
	// The rig's Domain is verified and lists no nameservers, so a ready zone
	// leaves it without Datum DNS authority.
	zone := newDNSZone(rig.gateway.Namespace, "example-com", "example.com")
	require.NoError(t, rig.upstream.Create(rig.ctx, zone))
	var gw gatewayv1.Gateway
	require.NoError(t, rig.upstream.Get(rig.ctx, rig.gateway, &gw))
	hostname := string(*claimRigListener.Hostname)
	stamp := stampAgo(time.Hour)
	held := newHeldRecordSet(&gw, dnsRecordSetName(gw.Name, hostname), zone.Name, holdSetup{stamp: stamp})
	held.Annotations[annotationDNSHostname] = hostname
	require.NoError(t, rig.upstream.Create(rig.ctx, held))

	_, requeue := rig.reconcile()

	assert.InDelta(t, (holdPeriod - time.Hour).Seconds(), requeue.Seconds(), 5, "the gateway runs again when the wait ends")
	var kept dnsv1alpha1.DNSRecordSet
	require.NoError(t, rig.upstream.Get(rig.ctx, client.ObjectKeyFromObject(held), &kept), "the record set is kept")
	assert.Equal(t, stamp, kept.Annotations[annotationDNSAuthorityMissingSince])
}
